package autosnap

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

func requireIntegration(t *testing.T) {
	if !runIntegrationTests {
		t.Skip("run integration test with: go test -tags=integration ./...")
	}
}

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

	status, cmd = parseCheckpointMessage("autosnap: check: test passing-check output branch: main idle_seconds: 10")
	if status != "unknown" {
		t.Fatalf("expected unknown when passing appears in check output, got %s", status)
	}
	if cmd != "test passing-check output" {
		t.Fatalf("expected parsed check command, got %q", cmd)
	}
}

func TestCheckpointListSummary(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{
			name:    "generated message",
			message: "autosnap: passing checkpoint 2026-06-04T13:22:10 branch: feature/foo check: npm test idle_seconds: 60 base: abc1234",
			want:    "passing npm test",
		},
		{
			name:    "custom multiline message",
			message: "feat(autosnap): add command output logging\n\nbody line",
			want:    "feat(autosnap): add command output logging",
		},
		{
			name:    "empty message",
			message: "",
			want:    "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkpointListSummary(tt.message); got != tt.want {
				t.Fatalf("expected summary %q, got %q", tt.want, got)
			}
		})
	}
}

func TestStateFilePathUsesAbsoluteGitDir(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)

	withWorkingDir(t, filepath.Join(repo, "subdir"), func() {
		repoRoot, _, _, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		statePath, err := stateFilePath(repoRoot)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}

		expected := filepath.Join(repoRoot, ".git", "autosnap", "state.json")
		if statePath != expected {
			t.Fatalf("state path should be rooted at repository git dir, expected %s, got %s", expected, statePath)
		}
	})
}

func TestStateFilePathForLinkedWorktree(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	worktree := filepath.Join(t.TempDir(), "linked-worktree")
	runGit(t, repo, "worktree", "add", "--detach", worktree, "HEAD")

	defer runGit(t, repo, "worktree", "remove", worktree)

	withWorkingDir(t, worktree, func() {
		repoRoot, _, _, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository in worktree failed: %v", err)
		}

		statePath, err := stateFilePath(repoRoot)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}

		gitDirPath, err := gitDir(context.Background(), repoRoot)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}

		expected := filepath.Join(gitDirPath, "autosnap", "state.json")
		if statePath != expected {
			t.Fatalf("expected state path %s, got %s", expected, statePath)
		}
	})
}

func TestBackgroundLogPath(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)

	withWorkingDir(t, filepath.Join(repo, "subdir"), func() {
		repoRoot, _, _, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		statePath, err := stateFilePath(repoRoot)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}

		logPath, err := backgroundLogPath(repoRoot)
		if err != nil {
			t.Fatalf("backgroundLogPath failed: %v", err)
		}

		expected := filepath.Join(filepath.Dir(statePath), "autosnap.log")
		if logPath != expected {
			t.Fatalf("expected log path %s, got %s", expected, logPath)
		}
	})
}

func TestStatusIncludesDaemonStatus(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		daemonStatus, err := getDaemonStatus(repo)
		if err != nil {
			t.Fatalf("getDaemonStatus failed: %v", err)
		}
		if daemonStatus != "daemon: not running" {
			t.Fatalf("expected stopped daemon status, got %q", daemonStatus)
		}
	})
}

func TestStatusIncludesStaleDaemonStatus(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	runPath, err := runStatePath(repo)
	if err != nil {
		t.Fatalf("runStatePath failed: %v", err)
	}

	state := autosnapRunState{PID: 999999, RepoRoot: repo, BranchDisplay: "main", CheckCommand: "true", IdleSeconds: 60}
	if err := saveAutosnapRunState(runPath, state); err != nil {
		t.Fatalf("saveAutosnapRunState failed: %v", err)
	}

	withWorkingDir(t, repo, func() {
		daemonStatus, err := getDaemonStatus(repo)
		if err != nil {
			t.Fatalf("getDaemonStatus failed: %v", err)
		}
		expected := "daemon: stopped (stale pid=999999)"
		if daemonStatus != expected {
			t.Fatalf("expected %q, got %q", expected, daemonStatus)
		}
	})
}

func TestStatusIncludesStaleDaemonWhenRunTokenMismatches(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	runPath, err := runStatePath(repo)
	if err != nil {
		t.Fatalf("runStatePath failed: %v", err)
	}
	runState := autosnapRunState{
		PID:           os.Getpid(),
		RepoRoot:      repo,
		BranchDisplay: "main",
		CheckCommand:  "true",
		IdleSeconds:   60,
		RunToken:      "token-a",
		StartedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	if err := saveAutosnapRunState(runPath, runState); err != nil {
		t.Fatalf("saveAutosnapRunState failed: %v", err)
	}
	defer func() {
		_ = removeAutosnapRunState(runPath)
	}()

	originalReader := readProcessCommandLine
	readProcessCommandLine = func(pid int) (string, error) {
		return "autosnap start --daemon --run-token=token-b", nil
	}
	defer func() {
		readProcessCommandLine = originalReader
	}()

	withWorkingDir(t, repo, func() {
		daemonStatus, err := getDaemonStatus(repo)
		if err != nil {
			t.Fatalf("getDaemonStatus failed: %v", err)
		}
		expected := "daemon: stopped (stale pid=" + strconv.Itoa(os.Getpid()) + ")"
		if daemonStatus != expected {
			t.Fatalf("expected %q, got %q", expected, daemonStatus)
		}
	})
}

func TestStatusReturnsErrorOnCorruptState(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	statePath, err := stateFilePath(repo)
	if err != nil {
		t.Fatalf("stateFilePath failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir state dir failed: %v", err)
	}
	if err := os.WriteFile(statePath, []byte("{invalid json"), 0o644); err != nil {
		t.Fatalf("write corrupt state failed: %v", err)
	}

	withWorkingDir(t, repo, func() {
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newStatusCommand())
		root.SetArgs([]string{"status"})

		errBuf := &bytes.Buffer{}
		root.SetOut(errBuf)
		root.SetErr(errBuf)

		if err := root.Execute(); err == nil {
			t.Fatalf("expected status command to fail with corrupt state")
		} else if !strings.Contains(err.Error(), "failed to load autosnap state") {
			t.Fatalf("expected status to fail loading state, got: %v", err)
		}
	})
}

func TestStatusOutputIncludesDaemonLine(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newStatusCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"status"})

		if err := root.Execute(); err != nil {
			t.Fatalf("status command failed: %v", err)
		}

		if !strings.Contains(buf.String(), "daemon: not running") {
			t.Fatalf("expected status output to include daemon status, got: %q", buf.String())
		}
	})
}

func TestStopCommandRemovesStalePidState(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	runPath, err := runStatePath(repo)
	if err != nil {
		t.Fatalf("runStatePath failed: %v", err)
	}

	if err := saveAutosnapRunState(runPath, autosnapRunState{PID: 0, RepoRoot: repo, BranchRef: "main", CheckCommand: "true", IdleSeconds: 60, StartedAt: time.Now().UTC().Format(time.RFC3339)}); err != nil {
		t.Fatalf("saveAutosnapRunState failed: %v", err)
	}

	withWorkingDir(t, repo, func() {
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		cmd := newStopCommand()
		root.AddCommand(cmd)
		root.SetArgs([]string{"stop"})

		if err := root.Execute(); err != nil {
			t.Fatalf("stop command failed: %v", err)
		}
		if _, err := os.Stat(runPath); !os.IsNotExist(err) {
			t.Fatalf("expected stale run state removed, got err=%v", err)
		}
	})
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

func TestAutosnapRunStateLifecycle(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	runPath, err := runStatePath(repo)
	if err != nil {
		t.Fatalf("runStatePath failed: %v", err)
	}

	state := autosnapRunState{
		PID:             os.Getpid(),
		RepoRoot:        repo,
		BranchRef:       "feature/test",
		BranchDisplay:   "feature/test",
		CheckCommand:    "npm test",
		MsgSourceCmd:    "printf msg",
		MsgSourceCmdSet: true,
		IdleSeconds:     60,
		SnapshotMode:    snapshotModeStaged,
		WatchMode:       watchModePoll,
		PollInterval:    2 * time.Second,
		StartedAt:       time.Now().UTC().Format(time.RFC3339),
	}

	if err := saveAutosnapRunState(runPath, state); err != nil {
		t.Fatalf("saveAutosnapRunState failed: %v", err)
	}

	loaded, err := loadAutosnapRunState(runPath)
	if err != nil {
		t.Fatalf("loadAutosnapRunState failed: %v", err)
	}
	if !reflect.DeepEqual(state, loaded) {
		t.Fatalf("loaded run state does not match saved state")
	}

	if err := removeAutosnapRunState(runPath); err != nil {
		t.Fatalf("removeAutosnapRunState failed: %v", err)
	}
	if _, err := os.Stat(runPath); !os.IsNotExist(err) {
		t.Fatalf("expected run state file removed")
	}
}

func TestEnsureNoActiveRunForRepo(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	runPath, err := runStatePath(repo)
	if err != nil {
		t.Fatalf("runStatePath failed: %v", err)
	}
	defer func() {
		_ = removeAutosnapRunState(runPath)
	}()

	if err := ensureNoActiveRunForRepo(repo); err != nil {
		t.Fatalf("ensureNoActiveRunForRepo should succeed with no run state: %v", err)
	}

	if err := saveAutosnapRunState(runPath, autosnapRunState{PID: 0}); err != nil {
		t.Fatalf("saveAutosnapRunState failed: %v", err)
	}

	if err := ensureNoActiveRunForRepo(repo); err != nil {
		t.Fatalf("expected zero-pid run state to be cleaned up: %v", err)
	}

	if _, err := os.Stat(runPath); !os.IsNotExist(err) {
		t.Fatalf("expected run state file removed for stale pid")
	}

	if err := saveAutosnapRunState(runPath, autosnapRunState{PID: os.Getpid(), RepoRoot: repo}); err != nil {
		t.Fatalf("saveAutosnapRunState failed: %v", err)
	}
	if err := ensureNoActiveRunForRepo(repo); err == nil {
		t.Fatalf("expected ensureNoActiveRunForRepo to block when process is active")
	}

	if !isProcessAlive(os.Getpid()) {
		t.Fatalf("expected current process to be reported as alive")
	}
}

func TestAutosnapCommandLineIdentityMatch(t *testing.T) {
	if !isAutosnapCommandLine("autosnap start --daemon --run-token=abc123", "abc123") {
		t.Fatal("expected matching token to be recognized as autosnap command")
	}
	if isAutosnapCommandLine("autosnap start --run-token=abc123", "abc123") {
		t.Fatal("expected missing --daemon to be rejected")
	}
	if isAutosnapCommandLine("autosnap start --daemon --run-token=wrong", "abc123") {
		t.Fatal("expected mismatched run token to be rejected")
	}
}

func TestEnsureNoActiveRunForRepoHonorsRunTokenMismatch(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	runPath, err := runStatePath(repo)
	if err != nil {
		t.Fatalf("runStatePath failed: %v", err)
	}
	defer func() {
		_ = removeAutosnapRunState(runPath)
	}()

	token := "token-xyz"
	originalReader := readProcessCommandLine
	readProcessCommandLine = func(pid int) (string, error) {
		return "autosnap start --foreground --daemon --run-token=" + token, nil
	}
	defer func() {
		readProcessCommandLine = originalReader
	}()

	if err := saveAutosnapRunState(runPath, autosnapRunState{
		PID:      os.Getpid(),
		RepoRoot: repo,
		RunToken: token,
	}); err != nil {
		t.Fatalf("saveAutosnapRunState failed: %v", err)
	}

	if err := ensureNoActiveRunForRepo(repo); err == nil {
		t.Fatalf("expected ensureNoActiveRunForRepo to block for matching run token")
	}

	readProcessCommandLine = func(pid int) (string, error) {
		return "autosnap start --daemon --run-token=other-token", nil
	}

	if err := saveAutosnapRunState(runPath, autosnapRunState{
		PID:      os.Getpid(),
		RepoRoot: repo,
		RunToken: token,
	}); err != nil {
		t.Fatalf("saveAutosnapRunState failed: %v", err)
	}
	if err := ensureNoActiveRunForRepo(repo); err != nil {
		t.Fatalf("expected stale run state with mismatched token to be cleaned: %v", err)
	}
	if _, err := os.Stat(runPath); !os.IsNotExist(err) {
		t.Fatalf("expected run state removed for token mismatch")
	}
}

func TestGitIgnoredPathsAreIgnored(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	gitignorePath := filepath.Join(repo, ".gitignore")
	if err := os.WriteFile(gitignorePath, []byte("build-output/\n*.tmp\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore failed: %v", err)
	}

	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		statePath, err := stateFilePath(repo)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}

		runner, err := newSnapshotRunner(context.Background(), repo, branchRef, "true", "", snapshotModeBoth, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunner failed: %v", err)
		}

		if !runner.shouldIgnorePath("build-output/file.bin") {
			t.Fatalf("expected build-output/file.bin to be ignored by gitignore")
		}

		if !runner.shouldIgnorePath("notes.tmp") {
			t.Fatalf("expected notes.tmp to be ignored by gitignore pattern")
		}

		if runner.shouldIgnorePath("notes.txt") {
			t.Fatalf("expected notes.txt to be tracked by watcher")
		}
	})
}

func TestAutosnapIgnoreRules(t *testing.T) {
	rules := []ignoreRule{
		{pattern: "tmp", directory: true},
		{pattern: "fixtures/*.pdf"},
		{pattern: "src/generated", anchored: true, directory: true},
		{pattern: "tmp/keep.txt", negated: true},
	}

	tests := []struct {
		path string
		want bool
	}{
		{path: "tmp", want: true},
		{path: "tmp/out.log", want: true},
		{path: "nested/tmp/out.log", want: true},
		{path: "tmp/keep.txt", want: false},
		{path: "fixtures/sample.pdf", want: true},
		{path: "other/fixtures/sample.pdf", want: false},
		{path: "src/generated/Foo.java", want: true},
		{path: "other/src/generated/Foo.java", want: false},
		{path: "src/main/Foo.java", want: false},
	}

	for _, tc := range tests {
		if got := matchAutosnapIgnoreRules(rules, tc.path); got != tc.want {
			t.Fatalf("matchAutosnapIgnoreRules(%q)=%v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestParseAutosnapIgnoreRule(t *testing.T) {
	tests := []struct {
		line string
		want ignoreRule
		ok   bool
	}{
		{line: "", ok: false},
		{line: "# comment", ok: false},
		{line: "/src/generated/", want: ignoreRule{pattern: "src/generated", anchored: true, directory: true}, ok: true},
		{line: "!tmp/keep.txt", want: ignoreRule{pattern: "tmp/keep.txt", negated: true}, ok: true},
		{line: "*.tmp", want: ignoreRule{pattern: "*.tmp"}, ok: true},
	}

	for _, tc := range tests {
		got, ok := parseIgnoreRule(tc.line)
		if ok != tc.ok {
			t.Fatalf("parseIgnoreRule(%q) ok=%v, want %v", tc.line, ok, tc.ok)
		}
		if ok && got != tc.want {
			t.Fatalf("parseIgnoreRule(%q)=%+v, want %+v", tc.line, got, tc.want)
		}
	}
}

func TestAutosnapIgnoreIsWatchOnlyForPolling(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".autosnapignore"), []byte("ignored/\n"), 0o644); err != nil {
		t.Fatalf("write .autosnapignore failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "ignored"), 0o755); err != nil {
		t.Fatalf("mkdir ignored failed: %v", err)
	}
	runGit(t, repo, "add", ".autosnapignore")
	runGit(t, repo, "commit", "-m", "add autosnapignore")

	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		statePath, err := stateFilePath(repo)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}
		runner, err := newSnapshotRunnerWithWatch(context.Background(), repo, branchRef, "true", "", snapshotModeBoth, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunnerWithWatch failed: %v", err)
		}
		if !runner.shouldIgnorePath("ignored/file.txt") {
			t.Fatalf("expected .autosnapignore path to be ignored by watcher")
		}

		if err := os.WriteFile(filepath.Join(repo, "ignored", "file.txt"), []byte("ignored change"), 0o644); err != nil {
			t.Fatalf("write ignored file failed: %v", err)
		}
		signature, err := runner.pollChangeSignature()
		if err != nil {
			t.Fatalf("pollChangeSignature failed: %v", err)
		}
		if signature != "" {
			t.Fatalf("expected ignored-only poll signature to be empty, got %q", signature)
		}

		if err := os.WriteFile(filepath.Join(repo, "watched.txt"), []byte("watched change"), 0o644); err != nil {
			t.Fatalf("write watched file failed: %v", err)
		}
		signature, err = runner.pollChangeSignature()
		if err != nil {
			t.Fatalf("pollChangeSignature failed: %v", err)
		}
		if !strings.Contains(signature, "watched.txt") || strings.Contains(signature, "ignored/file.txt") {
			t.Fatalf("unexpected poll signature: %q", signature)
		}

		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		commit, _, err := createCheckpoint(context.Background(), repo, branchRef, "true", time.Second, tree, "")
		if err != nil {
			t.Fatalf("createCheckpoint failed: %v", err)
		}
		if got := runGitOutput(t, repo, "show", commit+":ignored/file.txt"); got != "ignored change" {
			t.Fatalf("expected ignored file to remain in checkpoint, got %q", got)
		}
	})
}

func TestPollingDetectsRepeatedWorkingFileContentChanges(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)

	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		statePath, err := stateFilePath(repo)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}
		runner, err := newSnapshotRunnerWithWatch(context.Background(), repo, branchRef, "true", "", snapshotModeBoth, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunnerWithWatch failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("first dirty content"), 0o644); err != nil {
			t.Fatalf("write first dirty content failed: %v", err)
		}
		firstSignature, err := runner.pollChangeSignature()
		if err != nil {
			t.Fatalf("pollChangeSignature first failed: %v", err)
		}
		if firstSignature == "" {
			t.Fatalf("expected first dirty signature")
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("second dirty content"), 0o644); err != nil {
			t.Fatalf("write second dirty content failed: %v", err)
		}
		secondSignature, err := runner.pollChangeSignature()
		if err != nil {
			t.Fatalf("pollChangeSignature second failed: %v", err)
		}
		if secondSignature == firstSignature {
			t.Fatalf("expected repeated dirty file edit to change poll signature")
		}
	})
}

func TestPollingDetectsRepeatedUntrackedFileContentChanges(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)

	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		statePath, err := stateFilePath(repo)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}
		runner, err := newSnapshotRunnerWithWatch(context.Background(), repo, branchRef, "true", "", snapshotModeWorking, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunnerWithWatch failed: %v", err)
		}

		untrackedPath := filepath.Join(repo, "notes.txt")
		if err := os.WriteFile(untrackedPath, []byte("first note"), 0o644); err != nil {
			t.Fatalf("write first untracked content failed: %v", err)
		}
		firstSignature, err := runner.pollChangeSignature()
		if err != nil {
			t.Fatalf("pollChangeSignature first failed: %v", err)
		}
		if firstSignature == "" {
			t.Fatalf("expected untracked signature")
		}

		if err := os.WriteFile(untrackedPath, []byte("second note"), 0o644); err != nil {
			t.Fatalf("write second untracked content failed: %v", err)
		}
		secondSignature, err := runner.pollChangeSignature()
		if err != nil {
			t.Fatalf("pollChangeSignature second failed: %v", err)
		}
		if secondSignature == firstSignature {
			t.Fatalf("expected repeated untracked edit to change poll signature")
		}
	})
}

func TestPollingDetectsRepeatedStagedContentChanges(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)

	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		statePath, err := stateFilePath(repo)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}
		runner, err := newSnapshotRunnerWithWatch(context.Background(), repo, branchRef, "true", "", snapshotModeStaged, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunnerWithWatch failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("first staged content"), 0o644); err != nil {
			t.Fatalf("write first staged content failed: %v", err)
		}
		runGit(t, repo, "add", "file.txt")
		firstSignature, err := runner.pollChangeSignature()
		if err != nil {
			t.Fatalf("pollChangeSignature first failed: %v", err)
		}
		if firstSignature == "" {
			t.Fatalf("expected staged signature")
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("second staged content"), 0o644); err != nil {
			t.Fatalf("write second staged content failed: %v", err)
		}
		runGit(t, repo, "add", "file.txt")
		secondSignature, err := runner.pollChangeSignature()
		if err != nil {
			t.Fatalf("pollChangeSignature second failed: %v", err)
		}
		if secondSignature == firstSignature {
			t.Fatalf("expected repeated staged content change to change poll signature")
		}
	})
}

func TestStartCommandWatchFlagValidation(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newStartCommand())
		root.SetArgs([]string{"start", "--foreground", "--check", "true", "--watch-mode", "bad"})
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "invalid --watch-mode") {
			t.Fatalf("expected invalid watch mode error, got %v", err)
		}

		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newStartCommand())
		root.SetArgs([]string{"start", "--foreground", "--check", "true", "--watch-mode", "poll", "--poll-interval", "0s"})
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--poll-interval must be greater than 0") {
			t.Fatalf("expected invalid poll interval error, got %v", err)
		}
	})
}

func TestLoadAutosnapConfig(t *testing.T) {
	repo := t.TempDir()
	cfg, found, err := loadAutosnapConfig(repo)
	if err != nil {
		t.Fatalf("load missing config failed: %v", err)
	}
	if found {
		t.Fatalf("expected missing config to report found=false")
	}
	if cfg.Check != "" {
		t.Fatalf("expected zero config for missing file, got %+v", cfg)
	}

	raw := []byte(`check = "go test ./..."
idle_seconds = 15
snapshot_mode = "staged"
msg_source_cmd = "printf msg"

[watch]
mode = "auto"
poll_interval = "2s"
`)
	if err := os.WriteFile(autosnapConfigPath(repo), raw, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	cfg, found, err = loadAutosnapConfig(repo)
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if !found {
		t.Fatalf("expected config to be found")
	}
	if cfg.Check != "go test ./..." || cfg.IdleSeconds != 15 || cfg.SnapshotMode != snapshotModeStaged || cfg.MsgSourceCmd != "printf msg" || cfg.Watch.Mode != watchModeAuto || cfg.Watch.PollInterval != 2*time.Second {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestResolveStartConfigPrefersFlagsOverConfig(t *testing.T) {
	repo := t.TempDir()
	raw := []byte(`check = "go test ./..."
idle_seconds = 15
snapshot_mode = "staged"

[watch]
mode = "poll"
poll_interval = "2s"
`)
	if err := os.WriteFile(autosnapConfigPath(repo), raw, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	cmd := newStartCommand()
	if err := cmd.Flags().Set("check", "make test"); err != nil {
		t.Fatalf("set check flag failed: %v", err)
	}
	if err := cmd.Flags().Set("idle", "30"); err != nil {
		t.Fatalf("set idle flag failed: %v", err)
	}
	if err := cmd.Flags().Set("watch-mode", "auto"); err != nil {
		t.Fatalf("set watch-mode flag failed: %v", err)
	}

	cfg, found, err := resolveStartConfig(repo, cmd, "make test", "", 30, snapshotModeBoth, watchModeAuto, defaultPollInterval)
	if err != nil {
		t.Fatalf("resolve config failed: %v", err)
	}
	if !found {
		t.Fatalf("expected config to be found")
	}
	if cfg.Check != "make test" || cfg.IdleSeconds != 30 || cfg.Watch.Mode != watchModeAuto {
		t.Fatalf("expected flags to override config, got %+v", cfg)
	}
	if cfg.SnapshotMode != snapshotModeStaged || cfg.Watch.PollInterval != 2*time.Second {
		t.Fatalf("expected config values to remain for unset flags, got %+v", cfg)
	}
}

func TestRootCommandRegistersRestart(t *testing.T) {
	root := NewRootCommand()
	for _, cmd := range root.Commands() {
		if cmd.Name() == "restart" {
			return
		}
	}
	t.Fatalf("expected root command to register restart")
}

func TestResolveRestartConfigUsesActiveRunStateByDefault(t *testing.T) {
	repo := t.TempDir()
	raw := []byte(`check = "go test ./..."
idle_seconds = 15
snapshot_mode = "working"
msg_source_cmd = "printf config"

[watch]
mode = "recursive"
poll_interval = "5s"
`)
	if err := os.WriteFile(autosnapConfigPath(repo), raw, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	runState := autosnapRunState{
		CheckCommand:    "make verify",
		MsgSourceCmd:    "",
		MsgSourceCmdSet: true,
		IdleSeconds:     45,
		SnapshotMode:    snapshotModeStaged,
		WatchMode:       watchModePoll,
		PollInterval:    2 * time.Second,
	}
	cfg, err := resolveRestartConfig(repo, newRestartCommand(), runState, true, "", "", 60, snapshotModeBoth, watchModeRecursive, defaultPollInterval)
	if err != nil {
		t.Fatalf("resolve restart config failed: %v", err)
	}
	if cfg.Check != "make verify" || cfg.MsgSourceCmd != "" || cfg.IdleSeconds != 45 || cfg.SnapshotMode != snapshotModeStaged || cfg.Watch.Mode != watchModePoll || cfg.Watch.PollInterval != 2*time.Second {
		t.Fatalf("expected active run state to win, got %+v", cfg)
	}
}

func TestResolveRestartConfigFlagsOverrideActiveRunState(t *testing.T) {
	repo := t.TempDir()
	runState := autosnapRunState{
		CheckCommand:    "make verify",
		MsgSourceCmd:    "printf old",
		MsgSourceCmdSet: true,
		IdleSeconds:     45,
		SnapshotMode:    snapshotModeStaged,
		WatchMode:       watchModePoll,
		PollInterval:    2 * time.Second,
	}
	cmd := newRestartCommand()
	if err := cmd.Flags().Set("check", "npm test"); err != nil {
		t.Fatalf("set check flag failed: %v", err)
	}
	if err := cmd.Flags().Set("msg-source-cmd", "printf new"); err != nil {
		t.Fatalf("set msg-source-cmd flag failed: %v", err)
	}
	if err := cmd.Flags().Set("idle", "30"); err != nil {
		t.Fatalf("set idle flag failed: %v", err)
	}
	if err := cmd.Flags().Set("watch-mode", "auto"); err != nil {
		t.Fatalf("set watch-mode flag failed: %v", err)
	}
	if err := cmd.Flags().Set("poll-interval", "3s"); err != nil {
		t.Fatalf("set poll-interval flag failed: %v", err)
	}

	cfg, err := resolveRestartConfig(repo, cmd, runState, true, "npm test", "printf new", 30, snapshotModeBoth, watchModeAuto, 3*time.Second)
	if err != nil {
		t.Fatalf("resolve restart config failed: %v", err)
	}
	if cfg.Check != "npm test" || cfg.MsgSourceCmd != "printf new" || cfg.IdleSeconds != 30 || cfg.Watch.Mode != watchModeAuto || cfg.Watch.PollInterval != 3*time.Second {
		t.Fatalf("expected flags to override active run state, got %+v", cfg)
	}
	if cfg.SnapshotMode != snapshotModeStaged {
		t.Fatalf("expected unflagged snapshot mode from run state, got %+v", cfg)
	}
}

func TestResolveRestartConfigLegacyRunStateUsesConfigBeforeDefaults(t *testing.T) {
	repo := t.TempDir()
	raw := []byte(`check = "go test ./..."
idle_seconds = 15
snapshot_mode = "working"
msg_source_cmd = "printf config"

[watch]
mode = "auto"
poll_interval = "4s"
`)
	if err := os.WriteFile(autosnapConfigPath(repo), raw, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	runState := autosnapRunState{
		CheckCommand: "make verify",
		IdleSeconds:  45,
	}
	cfg, err := resolveRestartConfig(repo, newRestartCommand(), runState, true, "", "", 60, snapshotModeBoth, watchModeRecursive, defaultPollInterval)
	if err != nil {
		t.Fatalf("resolve restart config failed: %v", err)
	}
	if cfg.Check != "make verify" || cfg.IdleSeconds != 45 {
		t.Fatalf("expected present legacy run values to win, got %+v", cfg)
	}
	if cfg.MsgSourceCmd != "printf config" || cfg.SnapshotMode != snapshotModeWorking || cfg.Watch.Mode != watchModeAuto || cfg.Watch.PollInterval != 4*time.Second {
		t.Fatalf("expected missing legacy values from config before defaults, got %+v", cfg)
	}
}

func TestResolveRestartConfigNoActiveRunUsesStartConfig(t *testing.T) {
	repo := t.TempDir()
	raw := []byte(`check = "go test ./..."
idle_seconds = 15

[watch]
mode = "poll"
`)
	if err := os.WriteFile(autosnapConfigPath(repo), raw, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	cfg, err := resolveRestartConfig(repo, newRestartCommand(), autosnapRunState{}, false, "", "", 60, snapshotModeBoth, watchModeRecursive, defaultPollInterval)
	if err != nil {
		t.Fatalf("resolve restart config failed: %v", err)
	}
	if cfg.Check != "go test ./..." || cfg.IdleSeconds != 15 || cfg.Watch.Mode != watchModePoll || cfg.Watch.PollInterval != defaultPollInterval {
		t.Fatalf("expected inactive restart to use start config resolution, got %+v", cfg)
	}
}

func TestConfigInitAndShowCommands(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)

	withWorkingDir(t, repo, func() {
		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newConfigCommand())
		root.SetOut(buf)
		root.SetArgs([]string{"config", "init"})

		if err := root.Execute(); err != nil {
			t.Fatalf("config init failed: %v", err)
		}
		if _, err := os.Stat(autosnapConfigPath(repo)); err != nil {
			t.Fatalf("expected config file to exist: %v", err)
		}

		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newConfigCommand())
		root.SetArgs([]string{"config", "init"})
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("expected init to refuse overwrite, got %v", err)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newConfigCommand())
		root.SetOut(buf)
		root.SetArgs([]string{"config", "show"})
		if err := root.Execute(); err != nil {
			t.Fatalf("config show failed: %v", err)
		}
		expectedConfigPath, err := filepath.EvalSymlinks(autosnapConfigPath(repo))
		if err != nil {
			t.Fatalf("eval expected config path failed: %v", err)
		}
		out := buf.String()
		for _, want := range []string{
			"path: " + expectedConfigPath,
			"exists: true",
			"check: npm test",
			"watch.mode: recursive",
			"watch.poll_interval: 5s",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("expected config show output to contain %q, got %q", want, out)
			}
		}
	})
}

func TestStartCommandAcceptsConfigCheck(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	if err := os.WriteFile(autosnapConfigPath(repo), []byte("check = \"true\"\nidle_seconds = 1\n"), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	withWorkingDir(t, repo, func() {
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newStartCommand())
		root.SetArgs([]string{"start", "--foreground", "--watch-mode", "bad"})
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "invalid --watch-mode") {
			t.Fatalf("expected invalid watch mode error after config-provided check, got %v", err)
		}
	})
}

func TestStartDetachedArgsForwardWatchOptions(t *testing.T) {
	args := startDetachedArgs("/bin/autosnap", "make build", "printf msg", 30, snapshotModeBoth, watchModeAuto, 2*time.Second, "token")
	joined := strings.Join(args, "\n")
	for _, want := range []string{
		"--watch-mode\nauto",
		"--poll-interval\n2s",
		"--msg-source-cmd\nprintf msg",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected detached args to contain %q, got %v", want, args)
		}
	}
}

func TestWatchModeHelpers(t *testing.T) {
	if mode, err := normalizeWatchMode(""); err != nil || mode != watchModeRecursive {
		t.Fatalf("expected empty watch mode to normalize to recursive, got %q err=%v", mode, err)
	}
	if _, err := normalizeWatchMode("bad"); err == nil {
		t.Fatalf("expected invalid watch mode error")
	}
	if isWatchLimitError(os.ErrInvalid) {
		t.Fatalf("did not expect os.ErrInvalid to be recognized as watch limit")
	}
	if isWatchLimitError(nil) {
		t.Fatalf("did not expect nil to be recognized as watch limit")
	}
	if !isWatchLimitError(&os.PathError{Op: "open", Path: "x", Err: syscall.EMFILE}) {
		t.Fatalf("expected EMFILE to be recognized as watch limit")
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
	requireIntegration(t)
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
	requireIntegration(t)
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
	requireIntegration(t)
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

		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		ref1, _, err := createCheckpoint(context.Background(), repo, branchRef, "echo ok", 5*time.Second, tree, "")
		if err != nil {
			t.Fatalf("create first checkpoint failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("updated"), 0o644); err != nil {
			t.Fatalf("update file failed: %v", err)
		}

		time.Sleep(1 * time.Second)
		tree2, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		ref2, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree2, "")
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
		if checkpoints[0].Ref != ref1 {
			t.Fatalf("expected first listed ref %s, got %s", ref1, checkpoints[0].Ref)
		}
		if checkpoints[1].Ref != latestRef {
			t.Fatalf("expected last listed ref %s, got %s", latestRef, checkpoints[1].Ref)
		}
	})
}

func TestRunCheckUsesCurrentBranchOnEachRun(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ctx := context.Background()
		repoRoot, _, branchRef, err := detectRepository(ctx)
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		statePath, err := stateFilePath(repoRoot)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}

		runner, err := newSnapshotRunner(ctx, repoRoot, branchRef, "true", "", snapshotModeBoth, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunner failed: %v", err)
		}

		runner.runCheck()

		if runner.state.LastBranch != branchRef {
			t.Fatalf("expected initial checkpoint branch=%s, got %s", branchRef, runner.state.LastBranch)
		}

		if err := os.WriteFile(filepath.Join(repo, "other.txt"), []byte("change"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}

		runGit(t, repo, "checkout", "-b", "feature/from-main")

		runner.runCheck()

		if runner.state.LastBranch == branchRef {
			t.Fatalf("expected branch switch to be reflected in state. got branch=%s", runner.state.LastBranch)
		}

		latestMainRef, _, _, err := getLatestCheckpointForBranch(ctx, repoRoot, branchRef)
		if err != nil {
			t.Fatalf("getLatestCheckpointForBranch for %q failed: %v", branchRef, err)
		}
		if latestMainRef == "" {
			t.Fatalf("expected checkpoint for original branch %q", branchRef)
		}

		featureBranchRef := "feature/from-main"
		featureRef, _, _, err := getLatestCheckpointForBranch(ctx, repoRoot, featureBranchRef)
		if err != nil {
			t.Fatalf("getLatestCheckpointForBranch for %q failed: %v", featureBranchRef, err)
		}
		if featureRef == "" {
			t.Fatalf("expected checkpoint for switched branch %q", featureBranchRef)
		}

		expectedPrefix := snapshotRefPrefix(featureBranchRef)
		if !strings.HasPrefix(featureRef, expectedPrefix) {
			t.Fatalf("expected feature checkpoint ref to include %q prefix, got %q", expectedPrefix, featureRef)
		}
		if latestMainRef == featureRef {
			t.Fatalf("expected checkpoints to be isolated by branch, got shared ref %q", featureRef)
		}

	})
}

func TestListCommandBranchAndAllScopes(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		currentRef := createAutosnapTestCommitRef(t, repo, branchRef, "20200101T000000Z", "current branch checkpoint")
		featureRef := createAutosnapTestCommitRef(t, repo, "feature/foo", "20210101T000000Z", "feature branch checkpoint")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--branch", "feature/foo"})

		if err := root.Execute(); err != nil {
			t.Fatalf("branch list failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "20210101T000000Z") || !strings.Contains(output, "feature branch checkpoint") {
			t.Fatalf("expected feature checkpoint in branch list output, got %q", output)
		}
		if strings.Contains(output, currentRef) || strings.Contains(output, "current branch checkpoint") {
			t.Fatalf("expected branch list to exclude current checkpoint %s, got %q", currentRef, output)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--all"})

		if err := root.Execute(); err != nil {
			t.Fatalf("all list failed: %v", err)
		}

		output = buf.String()
		if !strings.Contains(output, branchRef) || !strings.Contains(output, "feature/foo") {
			t.Fatalf("expected all list output to include branch names, got %q", output)
		}
		if !strings.Contains(output, "current branch checkpoint") || !strings.Contains(output, "feature branch checkpoint") {
			t.Fatalf("expected all list output to include both checkpoint summaries, got %q", output)
		}
		if !strings.Contains(output, path.Base(currentRef)) || !strings.Contains(output, path.Base(featureRef)) {
			t.Fatalf("expected all list output to include both timestamps, got %q", output)
		}
	})
}

func TestListCommandRejectsMultipleScopes(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetArgs([]string{"list", "--branch", "feature/foo", "--all"})

		err := root.Execute()
		if err == nil {
			t.Fatalf("expected list to fail")
		}
		if !strings.Contains(err.Error(), "at most one scope") {
			t.Fatalf("expected scope error, got %v", err)
		}
	})
}

func TestRunCheckPassesMessageSourceEnvForFirstCheckpoint(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ctx := context.Background()
		repoRoot, _, branchRef, err := detectRepository(ctx)
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		head := runGitOutput(t, repoRoot, "rev-parse", "HEAD")

		statePath, err := stateFilePath(repoRoot)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}
		msgSourceCmd := `printf 'base:%s
prev:%s
branch:%s
head:%s
' "$AUTOSNAP_DIFF_BASE" "$AUTOSNAP_PREVIOUS_CHECKPOINT_REF" "$AUTOSNAP_BRANCH_REF" "$AUTOSNAP_HEAD"`
		runner, err := newSnapshotRunner(ctx, repoRoot, branchRef, "true", msgSourceCmd, snapshotModeBoth, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunner failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repoRoot, "first.txt"), []byte("first"), 0o644); err != nil {
			t.Fatalf("write first file failed: %v", err)
		}
		runner.runCheck()

		if runner.state.LastCheckpointRef == "" {
			t.Fatalf("expected checkpoint ref in state")
		}
		message, err := getCommitMessage(ctx, repoRoot, runner.state.LastCheckpointRef)
		if err != nil {
			t.Fatalf("getCommitMessage failed: %v", err)
		}
		for _, want := range []string{
			"base:" + head,
			"prev:",
			"branch:" + branchRef,
			"head:" + head,
		} {
			if !strings.Contains(message, want) {
				t.Fatalf("expected checkpoint message to contain %q, got %q", want, message)
			}
		}
	})
}

func TestRunCheckPassesPreviousCheckpointRefToMessageSource(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ctx := context.Background()
		repoRoot, _, branchRef, err := detectRepository(ctx)
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		statePath, err := stateFilePath(repoRoot)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}
		msgSourceCmd := `printf 'base:%s
prev:%s
' "$AUTOSNAP_DIFF_BASE" "$AUTOSNAP_PREVIOUS_CHECKPOINT_REF"`
		runner, err := newSnapshotRunner(ctx, repoRoot, branchRef, "true", msgSourceCmd, snapshotModeBoth, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunner failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repoRoot, "first.txt"), []byte("first"), 0o644); err != nil {
			t.Fatalf("write first file failed: %v", err)
		}
		runner.runCheck()
		firstRef := runner.state.LastCheckpointRef
		if firstRef == "" {
			t.Fatalf("expected first checkpoint ref")
		}

		time.Sleep(1 * time.Second)
		if err := os.WriteFile(filepath.Join(repoRoot, "second.txt"), []byte("second"), 0o644); err != nil {
			t.Fatalf("write second file failed: %v", err)
		}
		runner.runCheck()
		secondRef := runner.state.LastCheckpointRef
		if secondRef == "" || secondRef == firstRef {
			t.Fatalf("expected distinct second checkpoint ref, got first=%q second=%q", firstRef, secondRef)
		}

		message, err := getCommitMessage(ctx, repoRoot, secondRef)
		if err != nil {
			t.Fatalf("getCommitMessage failed: %v", err)
		}
		for _, want := range []string{
			"base:" + firstRef,
			"prev:" + firstRef,
		} {
			if !strings.Contains(message, want) {
				t.Fatalf("expected checkpoint message to contain %q, got %q", want, message)
			}
		}
	})
}

func TestRunCheckFallsBackToLatestCheckpointForMessageSourceEnv(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ctx := context.Background()
		repoRoot, _, branchRef, err := detectRepository(ctx)
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		gitDirectory, err := gitDir(ctx, repoRoot)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}
		tree, err := computeWorktreeTree(ctx, repoRoot, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		existingRef, _, err := createCheckpoint(ctx, repoRoot, branchRef, "true", time.Second, tree, "existing checkpoint")
		if err != nil {
			t.Fatalf("create existing checkpoint failed: %v", err)
		}

		statePath, err := stateFilePath(repoRoot)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}
		msgSourceCmd := `printf 'base:%s
prev:%s
' "$AUTOSNAP_DIFF_BASE" "$AUTOSNAP_PREVIOUS_CHECKPOINT_REF"`
		runner, err := newSnapshotRunner(ctx, repoRoot, branchRef, "true", msgSourceCmd, snapshotModeBoth, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunner failed: %v", err)
		}

		time.Sleep(1 * time.Second)
		if err := os.WriteFile(filepath.Join(repoRoot, "after-existing.txt"), []byte("change"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
		runner.runCheck()
		newRef := runner.state.LastCheckpointRef
		if newRef == "" || newRef == existingRef {
			t.Fatalf("expected new checkpoint ref, got existing=%q new=%q", existingRef, newRef)
		}

		message, err := getCommitMessage(ctx, repoRoot, newRef)
		if err != nil {
			t.Fatalf("getCommitMessage failed: %v", err)
		}
		for _, want := range []string{
			"base:" + existingRef,
			"prev:" + existingRef,
		} {
			if !strings.Contains(message, want) {
				t.Fatalf("expected checkpoint message to contain %q, got %q", want, message)
			}
		}
	})
}

func TestShowCommandResolvesCheckpointByTimestamp(t *testing.T) {
	requireIntegration(t)
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

		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}

		ref, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "")
		if err != nil {
			t.Fatalf("create checkpoint failed: %v", err)
		}

		timestamp := path.Base(ref)
		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"show", timestamp})

		if err := root.Execute(); err != nil {
			t.Fatalf("show command failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "checkpoint: "+ref) {
			t.Fatalf("expected output to include checkpoint ref, got %q", output)
		}
		if !strings.Contains(output, "status: passing") {
			t.Fatalf("expected output to include passing status, got %q", output)
		}
	})
}

func TestShowCommandResolvesCheckpointByRefAndCommit(t *testing.T) {
	requireIntegration(t)
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

		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		ref, commit, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "")
		if err != nil {
			t.Fatalf("create checkpoint failed: %v", err)
		}

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"show", ref})

		if err := root.Execute(); err != nil {
			t.Fatalf("show command with ref failed: %v", err)
		}
		if !strings.Contains(buf.String(), "checkpoint: "+ref) {
			t.Fatalf("expected output to include checkpoint ref, got %q", buf.String())
		}

		shortRef := strings.TrimSpace(commit[:7])
		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"show", shortRef})

		if err := root.Execute(); err != nil {
			t.Fatalf("show command with short commit failed: %v", err)
		}
		if !strings.Contains(buf.String(), "commit: "+shortRef) {
			t.Fatalf("expected output to include commit short hash, got %q", buf.String())
		}
		if !strings.Contains(buf.String(), "checkpoint: "+ref) {
			t.Fatalf("expected output to include checkpoint ref, got %q", buf.String())
		}
	})
}

func TestShowCommandReturnsNotFoundForUnknownCheckpoint(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetArgs([]string{"show", "does-not-exist"})

		if err := root.Execute(); err == nil {
			t.Fatalf("expected show to fail for unknown checkpoint")
		} else if !strings.Contains(err.Error(), "not found") && !strings.Contains(err.Error(), "no checkpoints") {
			t.Fatalf("expected checkpoint-not-found error, got: %v", err)
		}
	})
}

func TestShowCommandFullFlagShowsPatch(t *testing.T) {
	requireIntegration(t)
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

		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		_, _, err = createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "")
		if err != nil {
			t.Fatalf("create checkpoint failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("updated\n"), 0o644); err != nil {
			t.Fatalf("update file failed: %v", err)
		}

		tree2, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree changed failed: %v", err)
		}
		ref2, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree2, "")
		if err != nil {
			t.Fatalf("create checkpoint changed failed: %v", err)
		}

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"show", "--full", path.Base(ref2)})

		if err := root.Execute(); err != nil {
			t.Fatalf("show --full failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "diff --git") {
			t.Fatalf("expected patch output, got: %q", output)
		}
		if !strings.Contains(output, "checkpoint: "+ref2) {
			t.Fatalf("expected output to include checkpoint ref, got: %q", output)
		}
		if !strings.Contains(output, "+++ b/file.txt") {
			t.Fatalf("expected file diff in output, got: %q", output)
		}
	})
}

func TestShowCommandColorModes(t *testing.T) {
	requireIntegration(t)
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

		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		if _, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, ""); err != nil {
			t.Fatalf("create checkpoint failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("updated\n"), 0o644); err != nil {
			t.Fatalf("update file failed: %v", err)
		}

		tree2, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree changed failed: %v", err)
		}
		ref2, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree2, "")
		if err != nil {
			t.Fatalf("create checkpoint changed failed: %v", err)
		}

		fullArgs := []string{"show", "--full", "--color=always", path.Base(ref2)}
		colorBuf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(colorBuf)
		root.SetErr(colorBuf)
		root.SetArgs(fullArgs)

		if err := root.Execute(); err != nil {
			t.Fatalf("show --full --color=always failed: %v", err)
		}
		if !strings.Contains(colorBuf.String(), "\x1b[") {
			t.Fatalf("expected ANSI color escapes for --color=always, got: %q", colorBuf.String())
		}

		autoArgs := []string{"show", "--full", path.Base(ref2)}
		autoBuf := &bytes.Buffer{}
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(autoBuf)
		root.SetErr(autoBuf)
		root.SetArgs(autoArgs)

		if err := root.Execute(); err != nil {
			t.Fatalf("show --full (auto) failed: %v", err)
		}
		if strings.Contains(autoBuf.String(), "\x1b[") {
			t.Fatalf("expected auto mode to avoid ANSI when output is not a terminal: %q", autoBuf.String())
		}
	})
}

func TestRestoreCommandAppliesCheckpointToWorktreeAndIndex(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("checkpointed\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint content failed: %v", err)
		}
		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		ref, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "restore me")
		if err != nil {
			t.Fatalf("create checkpoint failed: %v", err)
		}
		runGit(t, repo, "reset", "--hard", "HEAD")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newRestoreCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"restore", path.Base(ref)})

		if err := root.Execute(); err != nil {
			t.Fatalf("restore command failed: %v", err)
		}

		got, err := os.ReadFile(filepath.Join(repo, "file.txt"))
		if err != nil {
			t.Fatalf("read restored file failed: %v", err)
		}
		if string(got) != "checkpointed\n" {
			t.Fatalf("expected restored file content, got %q", got)
		}
		runGit(t, repo, "diff", "--quiet")
		if status := runGitOutput(t, repo, "status", "--porcelain"); !strings.Contains(status, "M  file.txt") {
			t.Fatalf("expected staged restored change in status, got %q", status)
		}
	})
}

func TestRestoreCommandRefusesDirtyStateByDefault(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("checkpointed\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint content failed: %v", err)
		}
		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		ref, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "")
		if err != nil {
			t.Fatalf("create checkpoint failed: %v", err)
		}
		runGit(t, repo, "reset", "--hard", "HEAD")
		if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatalf("write dirty file failed: %v", err)
		}

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newRestoreCommand())
		root.SetArgs([]string{"restore", path.Base(ref)})

		err = root.Execute()
		if err == nil {
			t.Fatalf("expected restore to refuse dirty worktree")
		}
		if !strings.Contains(err.Error(), "clean worktree") {
			t.Fatalf("expected clean worktree error, got %v", err)
		}
	})
}

func TestRestoreCommandLeavesConflictMarkers(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("checkpointed\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint content failed: %v", err)
		}
		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		ref, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "")
		if err != nil {
			t.Fatalf("create checkpoint failed: %v", err)
		}
		runGit(t, repo, "reset", "--hard", "HEAD")
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("conflicting\n"), 0o644); err != nil {
			t.Fatalf("write conflicting file failed: %v", err)
		}
		runGit(t, repo, "add", "file.txt")
		runGit(t, repo, "commit", "-m", "conflicting branch commit")

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newRestoreCommand())
		root.SetArgs([]string{"restore", "--force", path.Base(ref)})

		err = root.Execute()
		if err == nil {
			t.Fatalf("expected restore to report merge conflict")
		}
		msg := err.Error()
		if !strings.Contains(msg, "failed to restore checkpoint") {
			t.Fatalf("expected restore context in error, got %v", err)
		}
		if !strings.Contains(msg, "conflict") && !strings.Contains(msg, "overlaps") {
			t.Fatalf("expected git conflict details in error, got %v", err)
		}

		content, readErr := os.ReadFile(filepath.Join(repo, "file.txt"))
		if readErr != nil {
			t.Fatalf("read conflicted file failed: %v", readErr)
		}
		if !strings.Contains(string(content), "<<<<<<<") {
			t.Fatalf("expected conflict markers in file, got %q", content)
		}
	})
}

func TestPromoteCommandCreatesBranchCommitFromCheckpoint(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		oldHead := runGitOutput(t, repo, "rev-parse", "HEAD")

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("promoted\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint content failed: %v", err)
		}
		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		message := "feat: promote checkpoint\n\nbody line"
		ref, checkpointCommit, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, message)
		if err != nil {
			t.Fatalf("create checkpoint failed: %v", err)
		}
		runGit(t, repo, "reset", "--hard", "HEAD")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPromoteCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"promote", path.Base(ref)})

		if err := root.Execute(); err != nil {
			t.Fatalf("promote command failed: %v", err)
		}

		newHead := runGitOutput(t, repo, "rev-parse", "HEAD")
		if newHead == oldHead {
			t.Fatalf("expected promote to create a new HEAD")
		}
		if parent := runGitOutput(t, repo, "rev-parse", "HEAD^"); parent != oldHead {
			t.Fatalf("expected promoted commit parent %s, got %s", oldHead, parent)
		}
		if promotedTree := runGitOutput(t, repo, "rev-parse", "HEAD^{tree}"); promotedTree != tree {
			t.Fatalf("expected promoted tree %s, got %s", tree, promotedTree)
		}
		checkpointTree := runGitOutput(t, repo, "rev-parse", checkpointCommit+"^{tree}")
		if promotedTree := runGitOutput(t, repo, "rev-parse", "HEAD^{tree}"); promotedTree != checkpointTree {
			t.Fatalf("expected promoted tree to match checkpoint tree %s, got %s", checkpointTree, promotedTree)
		}
		if got := runGitOutput(t, repo, "log", "-1", "--pretty=%B"); got != message {
			t.Fatalf("expected promoted message %q, got %q", message, got)
		}
		if status := runGitOutput(t, repo, "status", "--porcelain"); status != "" {
			t.Fatalf("expected clean status after promote, got %q", status)
		}
	})
}

func TestPromoteCommandNoOpsWhenCheckpointMatchesHead(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		oldHead := runGitOutput(t, repo, "rev-parse", "HEAD")

		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		ref, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "")
		if err != nil {
			t.Fatalf("create checkpoint failed: %v", err)
		}

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPromoteCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"promote", path.Base(ref)})

		if err := root.Execute(); err != nil {
			t.Fatalf("promote command failed: %v", err)
		}
		if newHead := runGitOutput(t, repo, "rev-parse", "HEAD"); newHead != oldHead {
			t.Fatalf("expected HEAD to stay %s, got %s", oldHead, newHead)
		}
		if !strings.Contains(buf.String(), "already matches HEAD") {
			t.Fatalf("expected no-op output, got %q", buf.String())
		}
	})
}

func TestParsePruneDuration(t *testing.T) {
	duration, err := parsePruneDuration("7d")
	if err != nil {
		t.Fatalf("parsePruneDuration day shorthand failed: %v", err)
	}
	if duration != 7*24*time.Hour {
		t.Fatalf("expected 7 days, got %s", duration)
	}

	duration, err = parsePruneDuration("24h")
	if err != nil {
		t.Fatalf("parsePruneDuration go duration failed: %v", err)
	}
	if duration != 24*time.Hour {
		t.Fatalf("expected 24 hours, got %s", duration)
	}

	if _, err := parsePruneDuration("-1h"); err == nil {
		t.Fatalf("expected negative duration to fail")
	}
}

func TestPruneCommandRejectsInvalidScopeAndPolicy(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		tests := []struct {
			name string
			args []string
			want string
		}{
			{
				name: "multiple scopes",
				args: []string{"prune", "--current-branch", "--all-branches", "--keep", "1"},
				want: "at most one scope",
			},
			{
				name: "missing policy",
				args: []string{"prune", "--current-branch"},
				want: "exactly one retention policy",
			},
			{
				name: "multiple policies",
				args: []string{"prune", "--current-branch", "--keep", "1", "--older-than", "7d"},
				want: "exactly one retention policy",
			},
			{
				name: "negative keep",
				args: []string{"prune", "--current-branch", "--keep", "-1"},
				want: "--keep must be non-negative",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
				root.AddCommand(newPruneCommand())
				root.SetArgs(tc.args)

				err := root.Execute()
				if err == nil {
					t.Fatalf("expected prune to fail")
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("expected error containing %q, got %v", tc.want, err)
				}
			})
		}
	})
}

func TestPruneCommandDryRunKeepsRefs(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		oldest := createAutosnapTestCommitRef(t, repo, branchRef, "20200101T000000Z", "old checkpoint message")
		middle := createAutosnapTestCommitRef(t, repo, branchRef, "20210101T000000Z", "middle checkpoint message")
		newest := createAutosnapTestRef(t, repo, branchRef, "20220101T000000Z")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPruneCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"prune", "--keep", "1"})

		if err := root.Execute(); err != nil {
			t.Fatalf("prune dry run failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "dry run: 2 checkpoint(s) would be pruned") {
			t.Fatalf("expected dry-run count, got %q", output)
		}
		if !strings.Contains(output, oldest) || !strings.Contains(output, middle) {
			t.Fatalf("expected old refs in dry-run output, got %q", output)
		}
		if !strings.Contains(output, "old checkpoint message") || !strings.Contains(output, "middle checkpoint message") {
			t.Fatalf("expected commit messages in dry-run output, got %q", output)
		}
		for _, ref := range []string{oldest, middle, newest} {
			if !gitRefExists(t, repo, ref) {
				t.Fatalf("expected dry-run to keep ref %s", ref)
			}
		}
	})
}

func TestPruneCommandApplyDeletesKeptOverflow(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		oldest := createAutosnapTestCommitRef(t, repo, branchRef, "20200101T000000Z", "old apply checkpoint")
		middle := createAutosnapTestCommitRef(t, repo, branchRef, "20210101T000000Z", "middle apply checkpoint")
		newest := createAutosnapTestRef(t, repo, branchRef, "20220101T000000Z")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPruneCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"prune", "--current-branch", "--keep", "1", "--apply"})

		if err := root.Execute(); err != nil {
			t.Fatalf("prune apply failed: %v", err)
		}
		if !strings.Contains(buf.String(), "pruned 2 checkpoint(s)") {
			t.Fatalf("expected applied count, got %q", buf.String())
		}
		if !strings.Contains(buf.String(), "old apply checkpoint") || !strings.Contains(buf.String(), "middle apply checkpoint") {
			t.Fatalf("expected applied output to include commit messages, got %q", buf.String())
		}
		if gitRefExists(t, repo, oldest) || gitRefExists(t, repo, middle) {
			t.Fatalf("expected old refs to be deleted")
		}
		if !gitRefExists(t, repo, newest) {
			t.Fatalf("expected newest ref to be kept")
		}
	})
}

func TestPruneCommandOlderThanSupportsDayScope(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		oldRef := createAutosnapTestRef(t, repo, branchRef, "20200101T000000Z")
		futureRef := createAutosnapTestRef(t, repo, branchRef, "29990101T000000Z")

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPruneCommand())
		root.SetArgs([]string{"prune", "--current-branch", "--older-than", "7d", "--apply"})

		if err := root.Execute(); err != nil {
			t.Fatalf("prune older-than failed: %v", err)
		}
		if gitRefExists(t, repo, oldRef) {
			t.Fatalf("expected old ref to be deleted")
		}
		if !gitRefExists(t, repo, futureRef) {
			t.Fatalf("expected future ref to be kept")
		}
	})
}

func TestPruneCommandBranchAndAllBranchScopes(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		currentRef := createAutosnapTestRef(t, repo, branchRef, "20200101T000000Z")
		featureRef := createAutosnapTestRef(t, repo, "feature/foo", "20200101T000000Z")

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPruneCommand())
		root.SetArgs([]string{"prune", "--branch", "feature/foo", "--keep", "0", "--apply"})

		if err := root.Execute(); err != nil {
			t.Fatalf("branch prune failed: %v", err)
		}
		if !gitRefExists(t, repo, currentRef) {
			t.Fatalf("expected current branch ref to remain")
		}
		if gitRefExists(t, repo, featureRef) {
			t.Fatalf("expected feature branch ref to be deleted")
		}

		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPruneCommand())
		root.SetArgs([]string{"prune", "--all-branches", "--keep", "0", "--apply"})

		if err := root.Execute(); err != nil {
			t.Fatalf("all-branches prune failed: %v", err)
		}
		if gitRefExists(t, repo, currentRef) {
			t.Fatalf("expected all-branches prune to delete current ref")
		}
	})
}

func TestComputeWorktreeTreeSnapshotModes(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}

		filePath := filepath.Join(repo, "file.txt")
		if err := os.WriteFile(filePath, []byte("staged"), 0o644); err != nil {
			t.Fatalf("write staged content failed: %v", err)
		}
		runGit(t, repo, "add", "file.txt")

		if err := os.WriteFile(filePath, []byte("unstaged"), 0o644); err != nil {
			t.Fatalf("write unstaged content failed: %v", err)
		}

		bothTree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree both failed: %v", err)
		}
		stagedTree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeStaged)
		if err != nil {
			t.Fatalf("computeWorktreeTree staged failed: %v", err)
		}
		workingTree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeWorking)
		if err != nil {
			t.Fatalf("computeWorktreeTree working failed: %v", err)
		}

		if got := testTreeFileContent(t, repo, bothTree, "file.txt"); got != "unstaged" {
			t.Fatalf("both mode should include unstaged working tree content, got %q", got)
		}
		if got := testTreeFileContent(t, repo, stagedTree, "file.txt"); got != "staged" {
			t.Fatalf("staged mode should include staged index content, got %q", got)
		}
		if got := testTreeFileContent(t, repo, workingTree, "file.txt"); got != "unstaged" {
			t.Fatalf("working mode should include working tree content, got %q", got)
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

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s failed: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
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

func testTreeFileContent(t *testing.T, repoRoot, tree, filePath string) string {
	t.Helper()
	content := runGitOutput(t, repoRoot, "show", tree+":"+filePath)
	return strings.TrimSuffix(content, "\n")
}

func createAutosnapTestRef(t *testing.T, repoRoot, branchRef, timestamp string) string {
	t.Helper()
	ref := snapshotRef(branchRef, timestamp)
	commit := runGitOutput(t, repoRoot, "rev-parse", "HEAD")
	runGit(t, repoRoot, "update-ref", ref, commit)
	return ref
}

func createAutosnapTestCommitRef(t *testing.T, repoRoot, branchRef, timestamp, message string) string {
	t.Helper()
	tree := runGitOutput(t, repoRoot, "rev-parse", "HEAD^{tree}")
	parent := runGitOutput(t, repoRoot, "rev-parse", "HEAD")
	result, err := runGitCommandWithInput(context.Background(), repoRoot, nil, message, "commit-tree", tree, "-p", parent, "-F", "-")
	if err != nil {
		t.Fatalf("create test commit failed: %v", err)
	}
	commit := strings.TrimSpace(result.Stdout)
	ref := snapshotRef(branchRef, timestamp)
	runGit(t, repoRoot, "update-ref", ref, commit)
	return ref
}

func gitRefExists(t *testing.T, repoRoot, ref string) bool {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--verify", ref)
	cmd.Dir = repoRoot
	return cmd.Run() == nil
}

func TestCreateCheckpointUsesCustomMessage(t *testing.T) {
	requireIntegration(t)
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

		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}

		customMessage := "line one\n\nline two"
		ref, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, customMessage)
		if err != nil {
			t.Fatalf("createCheckpoint failed: %v", err)
		}

		got := runGitOutput(t, repo, "log", "-1", "--pretty=%B", ref)
		if strings.TrimSpace(got) != customMessage {
			t.Fatalf("expected custom commit message %q, got %q", customMessage, got)
		}

		checkpoints, err := listCheckpointsForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("listCheckpointsForBranch failed: %v", err)
		}
		if len(checkpoints) != 1 {
			t.Fatalf("expected 1 checkpoint, got %d", len(checkpoints))
		}
		if checkpoints[0].Summary != "line one" {
			t.Fatalf("expected custom list summary %q, got %q", "line one", checkpoints[0].Summary)
		}
	})
}

func TestCreateCheckpointCheckedUsesExpectedHeadAsParent(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		position, err := currentGitPosition(context.Background(), repo)
		if err != nil {
			t.Fatalf("currentGitPosition failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("checkpointed"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}

		ref, _, err := createCheckpointChecked(context.Background(), repo, position.BranchRef, position.Head, "npm test", 5*time.Second, tree, "")
		if err != nil {
			t.Fatalf("createCheckpointChecked failed: %v", err)
		}

		parent := runGitOutput(t, repo, "rev-parse", ref+"^")
		if parent != position.Head {
			t.Fatalf("expected checkpoint parent %s, got %s", position.Head, parent)
		}
	})
}

func TestCreateCheckpointCheckedRejectsChangedHead(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		position, err := currentGitPosition(context.Background(), repo)
		if err != nil {
			t.Fatalf("currentGitPosition failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("committed"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
		runGit(t, repo, "add", "file.txt")
		runGit(t, repo, "commit", "-m", "commit between check and checkpoint")

		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}

		_, _, err = createCheckpointChecked(context.Background(), repo, position.BranchRef, position.Head, "npm test", 5*time.Second, tree, "")
		if err == nil {
			t.Fatalf("expected changed HEAD to reject checkpoint")
		}
		if !strings.Contains(err.Error(), "HEAD changed") {
			t.Fatalf("expected HEAD changed error, got %v", err)
		}

		ref, _, _, err := getLatestCheckpointForBranch(context.Background(), repo, position.BranchRef)
		if err != nil {
			t.Fatalf("getLatestCheckpointForBranch failed: %v", err)
		}
		if ref != "" {
			t.Fatalf("expected no checkpoint to be created, got %s", ref)
		}
	})
}

func TestCreateCheckpointFallsBackToGeneratedMessageForEmptyMessage(t *testing.T) {
	requireIntegration(t)
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

		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}

		ref, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "")
		if err != nil {
			t.Fatalf("createCheckpoint failed: %v", err)
		}

		msg := runGitOutput(t, repo, "log", "-1", "--pretty=%B", ref)
		if !strings.Contains(msg, "autosnap: passing checkpoint") {
			t.Fatalf("expected generated checkpoint message prefix, got %q", msg)
		}
	})
}
