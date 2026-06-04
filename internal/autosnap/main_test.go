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
		PID:           os.Getpid(),
		RepoRoot:      repo,
		BranchRef:     "feature/test",
		BranchDisplay: "feature/test",
		CheckCommand:  "npm test",
		IdleSeconds:   60,
		StartedAt:     time.Now().UTC().Format(time.RFC3339),
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
