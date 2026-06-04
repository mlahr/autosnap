package main

import (
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSnapshotRefNaming(t *testing.T) {
	prefix := snapshotRefPrefix("feature/foo")
	if prefix != path.Join("refs", "autosnapshots", "feature/foo") {
		t.Fatalf("unexpected prefix: %s", prefix)
	}

	ref := snapshotRef("feature/foo", "20260101T120000Z")
	if ref != path.Join("refs", "autosnapshots", "feature/foo", "20260101T120000Z") {
		t.Fatalf("unexpected ref: %s", ref)
	}
}

func TestParseCheckpointMessage(t *testing.T) {
	message := "autosnap: passing checkpoint 2026-06-04T13:22:10 branch: feature/foo check: npm test idle_seconds: 60 base: abc1234"

	status, cmd := parseCheckpointMessage(message)
	if status != "passing" {
		t.Fatalf("expected status passing, got %s", status)
	}
	if cmd != "npm test" {
		t.Fatalf("expected check command npm test, got %q", cmd)
	}

	status, cmd = parseCheckpointMessage("autosnap: failing checkpoint 2026-06-04T13:22:10")
	if status != "failing" {
		t.Fatalf("expected status failing, got %s", status)
	}
	if cmd != "" {
		t.Fatalf("expected empty check command, got %q", cmd)
	}
}

func TestStatePersistenceRoundTrip(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "autosnap", "state.json")
	state := autosnapState{
		RepoRoot:            "/tmp/repo",
		LastBranch:          "feature/foo",
		LastCheckpointRef:   "refs/autosnapshots/feature/foo/20260101T120000Z",
		LastCheckpointAt:    "2026-01-01 12:00:00",
		LastCheckpointTree:  "tree123",
		LastCheckAt:         "2026-01-01T12:00:00Z",
		LastCheckStatus:     "passed",
		LastCheckCommand:    "npm test",
		LastCheckDurationMs: 1234,
		LastFailureAt:       "2026-01-01T12:05:00Z",
		LastFailureExitCode: 1,
	}

	if err := saveAutosnapState(statePath, state); err != nil {
		t.Fatalf("saveAutosnapState failed: %v", err)
	}

	loaded, err := loadAutosnapState(statePath)
	if err != nil {
		t.Fatalf("loadAutosnapState failed: %v", err)
	}
	if !reflect.DeepEqual(state, loaded) {
		t.Fatalf("loaded state does not match saved state")
	}
}

func TestRunShellCheck(t *testing.T) {
	duration, code, err := runShellCheck(context.Background(), t.TempDir(), "true")
	if err != nil {
		t.Fatalf("expected true command to succeed, got %v", err)
	}
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if duration < 0 {
		t.Fatalf("expected non-negative duration")
	}

	_, code, err = runShellCheck(context.Background(), t.TempDir(), "false")
	if err == nil {
		t.Fatalf("expected false command to fail")
	}
	if code == 0 {
		t.Fatalf("expected non-zero exit code, got %d", code)
	}
}

func TestGitCommandResultCapturesExitCode(t *testing.T) {
	repo := createTestRepo(t)

	result, err := runGitCommand(context.Background(), repo, nil, "nonexistent")
	if err == nil {
		t.Fatalf("expected command to fail")
	}
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit code for failed git command")
	}
}

func TestDetectRepository(t *testing.T) {
	withWorkingDir(t, t.TempDir(), func() {
		_, _, _, err := detectRepository(context.Background())
		if err == nil {
			t.Fatalf("expected non-git directory to return error")
		}
	})

	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		root, branchDisplay, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		expRoot, err := filepath.EvalSymlinks(repo)
		if err != nil {
			t.Fatalf("eval repo root failed: %v", err)
		}
		gotRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			t.Fatalf("eval detected root failed: %v", err)
		}
		if gotRoot != expRoot {
			t.Fatalf("expected repo root %s, got %s", expRoot, gotRoot)
		}
		if branchDisplay == "" || branchRef == "" {
			t.Fatalf("expected non-empty branch values")
		}
	})

	withWorkingDir(t, filepath.Join(repo, "subdir"), func() {
		root, _, _, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed in subdir: %v", err)
		}
		if gotRoot, err := filepath.EvalSymlinks(root); err == nil {
			expRoot, expErr := filepath.EvalSymlinks(repo)
			if expErr != nil {
				t.Fatalf("eval repo root failed: %v", expErr)
			}
			if gotRoot != expRoot {
				t.Fatalf("expected root from subdir %s, got %s", expRoot, gotRoot)
			}
		} else {
			t.Fatalf("eval detected root failed: %v", err)
		}
	})
}

func TestGetLatestAndListCheckpointForBranch(t *testing.T) {
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}

		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		ref1, _, err := createCheckpoint(context.Background(), repo, branchRef, "echo ok", 5*time.Second, tree)
		if err != nil {
			t.Fatalf("create first checkpoint failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("updated"), 0o644); err != nil {
			t.Fatalf("update file failed: %v", err)
		}

		time.Sleep(1 * time.Second)
		tree2, err := computeWorktreeTree(context.Background(), repo, gitDirectory)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		ref2, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree2)
		if err != nil {
			t.Fatalf("create second checkpoint failed: %v", err)
		}
		if ref1 == ref2 {
			t.Fatalf("expected unique checkpoint refs, got duplicate %s", ref1)
		}

		latestRef, latestTs, latestCommit, err := getLatestCheckpointForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("getLatestCheckpointForBranch failed: %v", err)
		}
		if path.Base(latestRef) != path.Base(ref2) {
			t.Fatalf("expected latest ref base %s, got %s", path.Base(ref2), latestRef)
		}
		if latestTs == "" || latestCommit == "" {
			t.Fatalf("expected latest checkpoint timestamp/commit to be present")
		}

		checkpoints, err := listCheckpointsForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("listCheckpointsForBranch failed: %v", err)
		}
		if len(checkpoints) != 2 {
			t.Fatalf("expected 2 checkpoints, got %d", len(checkpoints))
		}
		if checkpoints[0].Ref != latestRef {
			t.Fatalf("expected first listed ref %s, got %s", latestRef, checkpoints[0].Ref)
		}
	})
}

func createTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.name", "Autosnap Test")
	runGit(t, dir, "config", "user.email", "test@autosnap.local")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("initial"), 0o644); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	runGit(t, dir, "add", "file.txt")
	runGit(t, dir, "commit", "-m", "initial commit")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, out)
	}
}

func withWorkingDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to %s failed: %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(prev); err != nil {
			t.Fatalf("restore chdir failed: %v", err)
		}
	})
	fn()
}
