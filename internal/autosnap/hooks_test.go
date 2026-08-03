package autosnap

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestManagedHookIntegrityAndShellQuoting(t *testing.T) {
	raw := renderManagedHook("/tmp/autosnap's/bin", "/tmp/hook backup", true)
	if !validManagedHook(raw) {
		t.Fatalf("expected generated hook to pass integrity validation:\n%s", raw)
	}
	if !bytes.Contains(raw, []byte(`'/tmp/autosnap'"'"'s/bin'`)) {
		t.Fatalf("expected executable path to be shell quoted, got:\n%s", raw)
	}
	tampered := append([]byte{}, raw...)
	tampered[len(tampered)-2] = '1'
	if validManagedHook(tampered) {
		t.Fatalf("expected modified hook to fail integrity validation")
	}
}

func TestAutosnapHookTargetsIncludePostCommit(t *testing.T) {
	targets := autosnapHookTargets("/tmp/hooks")
	names := make([]string, 0, len(targets))
	for _, target := range targets {
		names = append(names, target.name)
	}
	want := []string{"post-checkout", "pre-commit", "post-commit"}
	if !slices.Equal(names, want) {
		t.Fatalf("unexpected hook targets: got %v want %v", names, want)
	}
}

func TestHooksInstallStatusAndUninstall(t *testing.T) {
	repo := createTestRepo(t)
	writeTrackedHookTestConfig(t, repo)

	oldExecutable := autosnapExecutable
	oldEnsure := ensureAutosnapRunningForHooks
	autosnapExecutable = func() (string, error) { return "/opt/autosnap/bin/autosnap", nil }
	resolvedRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatalf("resolve repository path failed: %v", err)
	}
	ensureCalls := 0
	ensureAutosnapRunningForHooks = func(ctx context.Context, repoRoot string, out io.Writer) error {
		ensureCalls++
		if repoRoot != resolvedRepo {
			t.Fatalf("unexpected ensure repo: got %s want %s", repoRoot, resolvedRepo)
		}
		return nil
	}
	t.Cleanup(func() {
		autosnapExecutable = oldExecutable
		ensureAutosnapRunningForHooks = oldEnsure
	})

	withWorkingDir(t, repo, func() {
		var out bytes.Buffer
		if err := installAutosnapHooks(context.Background(), &out, &out, false); err != nil {
			t.Fatalf("install hooks failed: %v", err)
		}
		if ensureCalls != 1 {
			t.Fatalf("expected install to ensure current daemon once, got %d", ensureCalls)
		}

		hooksDir := filepath.Join(repo, ".git", "hooks")
		for _, target := range autosnapHookTargets(hooksDir) {
			raw, err := os.ReadFile(target.path)
			if err != nil {
				t.Fatalf("read installed %s hook failed: %v", target.name, err)
			}
			if !validManagedHook(raw) {
				t.Fatalf("expected %s to be an intact managed hook", target.name)
			}
			info, err := os.Stat(target.path)
			if err != nil || info.Mode().Perm() != 0o755 {
				t.Fatalf("expected executable %s hook, info=%v err=%v", target.name, info, err)
			}
		}

		out.Reset()
		if err := showAutosnapHooksStatus(context.Background(), &out); err != nil {
			t.Fatalf("hook status failed: %v", err)
		}
		if strings.Count(out.String(), ": installed\n") != 3 {
			t.Fatalf("unexpected installed status:\n%s", out.String())
		}

		if err := installAutosnapHooks(context.Background(), &out, &out, false); err != nil {
			t.Fatalf("idempotent install failed: %v", err)
		}
		if ensureCalls != 2 {
			t.Fatalf("expected reinstall to ensure current daemon, got %d calls", ensureCalls)
		}

		out.Reset()
		if err := uninstallAutosnapHooks(context.Background(), &out); err != nil {
			t.Fatalf("uninstall hooks failed: %v", err)
		}
		if !strings.Contains(out.String(), "running daemons were not stopped") {
			t.Fatalf("expected daemon lifecycle guidance, got %q", out.String())
		}
		for _, target := range autosnapHookTargets(hooksDir) {
			if _, err := os.Stat(target.path); !os.IsNotExist(err) {
				t.Fatalf("expected %s hook removed, got err=%v", target.name, err)
			}
		}
	})
}

func TestHooksInstallRequiresForceToBackUpAndChain(t *testing.T) {
	repo := createTestRepo(t)
	writeTrackedHookTestConfig(t, repo)
	hooksDir := filepath.Join(repo, ".git", "hooks")
	preCommit := filepath.Join(hooksDir, "pre-commit")
	existing := []byte("#!/bin/sh\nexit 7\n")
	if err := os.WriteFile(preCommit, existing, 0o755); err != nil {
		t.Fatalf("write existing hook failed: %v", err)
	}

	fakeAutosnap := filepath.Join(t.TempDir(), "autosnap")
	if err := os.WriteFile(fakeAutosnap, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake autosnap failed: %v", err)
	}
	oldExecutable := autosnapExecutable
	oldEnsure := ensureAutosnapRunningForHooks
	autosnapExecutable = func() (string, error) { return fakeAutosnap, nil }
	ensureAutosnapRunningForHooks = func(context.Context, string, io.Writer) error { return nil }
	t.Cleanup(func() {
		autosnapExecutable = oldExecutable
		ensureAutosnapRunningForHooks = oldEnsure
	})

	withWorkingDir(t, repo, func() {
		if err := installAutosnapHooks(context.Background(), os.Stdout, os.Stderr, false); err == nil || !strings.Contains(err.Error(), "--force") {
			t.Fatalf("expected existing hook to require force, got %v", err)
		}
		if raw, err := os.ReadFile(preCommit); err != nil || !bytes.Equal(raw, existing) {
			t.Fatalf("expected refused install to leave hook unchanged, raw=%q err=%v", raw, err)
		}

		if err := installAutosnapHooks(context.Background(), os.Stdout, os.Stderr, true); err != nil {
			t.Fatalf("forced install failed: %v", err)
		}
		backup := preCommit + hookBackupSuffix
		if raw, err := os.ReadFile(backup); err != nil || !bytes.Equal(raw, existing) {
			t.Fatalf("expected exact hook backup, raw=%q err=%v", raw, err)
		}

		cmd := exec.Command(preCommit)
		cmd.Dir = repo
		err := cmd.Run()
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() != 7 {
			t.Fatalf("expected chained hook exit 7, got %v", err)
		}

		if err := uninstallAutosnapHooks(context.Background(), os.Stdout); err != nil {
			t.Fatalf("uninstall chained hooks failed: %v", err)
		}
		if raw, err := os.ReadFile(preCommit); err != nil || !bytes.Equal(raw, existing) {
			t.Fatalf("expected original hook restored, raw=%q err=%v", raw, err)
		}
		if _, err := os.Stat(backup); !os.IsNotExist(err) {
			t.Fatalf("expected backup consumed during restore, got err=%v", err)
		}
	})
}

func TestHooksInstallCustomPathRequiresForce(t *testing.T) {
	repo := createTestRepo(t)
	writeTrackedHookTestConfig(t, repo)
	runGit(t, repo, "config", "core.hooksPath", ".custom-hooks")

	oldExecutable := autosnapExecutable
	oldEnsure := ensureAutosnapRunningForHooks
	autosnapExecutable = func() (string, error) { return "/opt/autosnap", nil }
	ensureAutosnapRunningForHooks = func(context.Context, string, io.Writer) error { return nil }
	t.Cleanup(func() {
		autosnapExecutable = oldExecutable
		ensureAutosnapRunningForHooks = oldEnsure
	})

	withWorkingDir(t, repo, func() {
		if err := installAutosnapHooks(context.Background(), os.Stdout, os.Stderr, false); err == nil || !strings.Contains(err.Error(), "core.hooksPath") {
			t.Fatalf("expected custom path refusal, got %v", err)
		}
		var warnings bytes.Buffer
		if err := installAutosnapHooks(context.Background(), os.Stdout, &warnings, true); err != nil {
			t.Fatalf("forced custom path install failed: %v", err)
		}
		if !strings.Contains(warnings.String(), "linked-worktree coverage") {
			t.Fatalf("expected custom path scope warning, got %q", warnings.String())
		}
		if _, err := os.Stat(filepath.Join(repo, ".custom-hooks", "post-checkout")); err != nil {
			t.Fatalf("expected hook in custom path: %v", err)
		}
	})
}

func TestPostCheckoutHookRunsAfterWorktreeFilesExistAndDoesNotFailGit(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	writeTrackedHookTestConfig(t, repo)
	runGit(t, repo, "commit", "-m", "add autosnap config")

	logPath := filepath.Join(t.TempDir(), "hook.log")
	fakeAutosnap := filepath.Join(t.TempDir(), "autosnap")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s|%%s|%%s\\n' \"$1\" \"$PWD\" \"$(test -f .autosnap.toml && printf present || printf missing)\" >> %s\nexit 1\n", shellQuote(logPath))
	if err := os.WriteFile(fakeAutosnap, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake autosnap failed: %v", err)
	}
	oldExecutable := autosnapExecutable
	oldEnsure := ensureAutosnapRunningForHooks
	autosnapExecutable = func() (string, error) { return fakeAutosnap, nil }
	ensureAutosnapRunningForHooks = func(context.Context, string, io.Writer) error { return nil }
	t.Cleanup(func() {
		autosnapExecutable = oldExecutable
		ensureAutosnapRunningForHooks = oldEnsure
	})

	worktree := filepath.Join(t.TempDir(), "linked")
	withWorkingDir(t, repo, func() {
		if err := installAutosnapHooks(context.Background(), os.Stdout, os.Stderr, false); err != nil {
			t.Fatalf("install hooks failed: %v", err)
		}
	})
	runGit(t, repo, "worktree", "add", "--detach", worktree, "HEAD")
	t.Cleanup(func() { runGit(t, repo, "worktree", "remove", "--force", worktree) })

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read hook log failed: %v", err)
	}
	resolvedWorktree, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatalf("resolve worktree path failed: %v", err)
	}
	want := "ensure-running|" + resolvedWorktree + "|present"
	if !strings.Contains(string(raw), want) {
		t.Fatalf("expected post-checkout after config checkout %q, got %q", want, raw)
	}
}

func TestModifiedManagedHookIsProtected(t *testing.T) {
	dir := t.TempDir()
	target := autosnapHookTargets(dir)[0]
	raw := renderManagedHook("/opt/autosnap", target.backupPath, false)
	raw = append(raw, []byte("# local edit\n")...)
	if err := os.WriteFile(target.path, raw, 0o755); err != nil {
		t.Fatalf("write modified managed hook failed: %v", err)
	}
	if got := hookTargetStatus(target); got != "modified" {
		t.Fatalf("expected modified status, got %q", got)
	}
	if err := preflightHookInstall(target, true); err == nil || !strings.Contains(err.Error(), "modified") {
		t.Fatalf("expected modified hook protection, got %v", err)
	}
}

func TestOrphanedHookBackupIsProtected(t *testing.T) {
	dir := t.TempDir()
	target := autosnapHookTargets(dir)[0]
	if err := os.WriteFile(target.backupPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write orphaned backup failed: %v", err)
	}
	if got := hookTargetStatus(target); got != "conflicting (orphaned backup)" {
		t.Fatalf("expected orphaned backup conflict, got %q", got)
	}
	if err := preflightHookInstall(target, true); err == nil || !strings.Contains(err.Error(), "backup path exists") {
		t.Fatalf("expected orphaned backup protection, got %v", err)
	}
}

func TestDanglingHookSymlinkRequiresForceAndIsRestored(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated Windows privileges")
	}
	repo := createTestRepo(t)
	writeTrackedHookTestConfig(t, repo)
	target := autosnapHookTargets(filepath.Join(repo, ".git", "hooks"))[0]
	wantLink := "missing-hook-target"
	if err := os.Symlink(wantLink, target.path); err != nil {
		t.Fatalf("create dangling hook symlink failed: %v", err)
	}
	oldExecutable := autosnapExecutable
	oldEnsure := ensureAutosnapRunningForHooks
	autosnapExecutable = func() (string, error) { return "/opt/autosnap", nil }
	ensureAutosnapRunningForHooks = func(context.Context, string, io.Writer) error { return nil }
	t.Cleanup(func() {
		autosnapExecutable = oldExecutable
		ensureAutosnapRunningForHooks = oldEnsure
	})

	withWorkingDir(t, repo, func() {
		if got := hookTargetStatus(target); got != "conflicting" {
			t.Fatalf("expected dangling symlink conflict, got %q", got)
		}
		if err := installAutosnapHooks(context.Background(), io.Discard, io.Discard, false); err == nil || !strings.Contains(err.Error(), "--force") {
			t.Fatalf("expected dangling symlink to require force, got %v", err)
		}
		if info, err := os.Lstat(target.path); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("expected refused install to preserve symlink, info=%v err=%v", info, err)
		}

		if err := installAutosnapHooks(context.Background(), io.Discard, io.Discard, true); err != nil {
			t.Fatalf("forced dangling symlink install failed: %v", err)
		}
		if raw, err := os.ReadFile(target.path); err != nil || !validManagedHook(raw) {
			t.Fatalf("expected managed replacement hook, err=%v", err)
		}
		if link, err := os.Readlink(target.backupPath); err != nil || link != wantLink {
			t.Fatalf("expected dangling symlink backup %q, got %q err=%v", wantLink, link, err)
		}

		if err := uninstallAutosnapHooks(context.Background(), io.Discard); err != nil {
			t.Fatalf("uninstall hooks failed: %v", err)
		}
		if link, err := os.Readlink(target.path); err != nil || link != wantLink {
			t.Fatalf("expected restored dangling symlink %q, got %q err=%v", wantLink, link, err)
		}
	})
}

func writeTrackedHookTestConfig(t *testing.T, repo string) {
	t.Helper()
	if err := os.WriteFile(autosnapConfigPath(repo), []byte("check = \"true\"\n[watch]\nmode = \"poll\"\n"), 0o644); err != nil {
		t.Fatalf("write autosnap config failed: %v", err)
	}
	runGit(t, repo, "add", autosnapConfigFileName)
}
