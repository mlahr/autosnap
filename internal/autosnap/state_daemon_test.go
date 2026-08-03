package autosnap

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestStatusOutputsFormattedLastCheckpoint(t *testing.T) {
	t.Parallel()
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
		state := autosnapState{
			RepoRoot:         repo,
			LastBranch:       branchRef,
			LastCheckpointAt: "20260101T120000Z",
		}
		if err := saveAutosnapState(statePath, state); err != nil {
			t.Fatalf("saveAutosnapState failed: %v", err)
		}

		parsed, err := time.Parse("20060102T150405Z", "20260101T120000Z")
		if err != nil {
			t.Fatalf("parse timestamp failed: %v", err)
		}
		want := parsed.Local().Format("2006-01-02 15:04:05 MST")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newStatusCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"status"})
		if err := root.Execute(); err != nil {
			t.Fatalf("status command failed: %v", err)
		}
		if !strings.Contains(buf.String(), "last checkpoint: "+want) {
			t.Fatalf("expected formatted status checkpoint timestamp, got: %q", buf.String())
		}
		if strings.Contains(buf.String(), "last checkpoint: 20260101T120000Z") {
			t.Fatalf("expected formatted status checkpoint timestamp, got: %q", buf.String())
		}
	})
}

func TestStopCommandRemovesStalePidState(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
		SnapshotMode:    snapshotModeBoth,
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

func TestWriteLogTail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "autosnap.log")
	if err := os.WriteFile(logPath, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatalf("write log failed: %v", err)
	}

	var buf bytes.Buffer
	offset, err := writeLogTail(&buf, logPath, 2)
	if err != nil {
		t.Fatalf("writeLogTail failed: %v", err)
	}
	if got, want := buf.String(), "two\nthree\n"; got != want {
		t.Fatalf("expected tail %q, got %q", want, got)
	}
	if offset != int64(len("one\ntwo\nthree\n")) {
		t.Fatalf("expected offset at end of log, got %d", offset)
	}

	buf.Reset()
	if _, err := writeLogTail(&buf, logPath, 0); err != nil {
		t.Fatalf("writeLogTail -n 0 failed: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected empty output for -n 0, got %q", buf.String())
	}

	buf.Reset()
	if _, err := writeLogTail(&buf, logPath, -1); err != nil {
		t.Fatalf("writeLogTail all failed: %v", err)
	}
	if got, want := buf.String(), "one\ntwo\nthree\n"; got != want {
		t.Fatalf("expected full log %q, got %q", want, got)
	}
}

func TestWriteLogTailMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, err := writeLogTail(&bytes.Buffer{}, filepath.Join(dir, "missing.log"), -1)
	if err == nil || !strings.Contains(err.Error(), "autosnap log not found") {
		t.Fatalf("expected missing log error, got %v", err)
	}
}

func TestCompactLogFileNoopsWhenUnderLimitOrMissing(t *testing.T) {
	dir := t.TempDir()
	missingPath := filepath.Join(dir, "missing.log")
	if err := compactLogFile(missingPath, 10); err != nil {
		t.Fatalf("compactLogFile missing file failed: %v", err)
	}

	logPath := filepath.Join(dir, "autosnap.log")
	if err := os.WriteFile(logPath, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatalf("write log failed: %v", err)
	}
	if err := compactLogFile(logPath, 100); err != nil {
		t.Fatalf("compactLogFile under limit failed: %v", err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read compacted log failed: %v", err)
	}
	if got, want := string(raw), "one\ntwo\n"; got != want {
		t.Fatalf("expected log to remain %q, got %q", want, got)
	}
}

func TestCompactLogFileKeepsNewestCompleteLines(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "autosnap.log")
	if err := os.WriteFile(logPath, []byte("alpha\nbeta\ngamma\ndelta\n"), 0o644); err != nil {
		t.Fatalf("write log failed: %v", err)
	}

	if err := compactLogFile(logPath, int64(len("ta\ngamma\ndelta\n"))); err != nil {
		t.Fatalf("compactLogFile failed: %v", err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read compacted log failed: %v", err)
	}
	if got, want := string(raw), "gamma\ndelta\n"; got != want {
		t.Fatalf("expected compacted log %q, got %q", want, got)
	}
}

func TestFollowLogWritesAppends(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "autosnap.log")
	if err := os.WriteFile(logPath, []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write log failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		time.Sleep(20 * time.Millisecond)
		file, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return
		}
		_, _ = file.WriteString("next\n")
		_ = file.Close()
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	var buf bytes.Buffer
	if err := followLog(ctx, &buf, logPath, int64(len("base\n")), 10*time.Millisecond); err != nil {
		t.Fatalf("followLog failed: %v", err)
	}
	if got, want := buf.String(), "next\n"; got != want {
		t.Fatalf("expected followed output %q, got %q", want, got)
	}
}

func TestLogsCommandTail(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		logPath, err := backgroundLogPath(repo)
		if err != nil {
			t.Fatalf("backgroundLogPath failed: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			t.Fatalf("mkdir log dir failed: %v", err)
		}
		if err := os.WriteFile(logPath, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
			t.Fatalf("write log failed: %v", err)
		}

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newLogsCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"logs", "-n", "2"})

		if err := root.Execute(); err != nil {
			t.Fatalf("logs command failed: %v", err)
		}
		if got, want := buf.String(), "beta\ngamma\n"; got != want {
			t.Fatalf("expected logs output %q, got %q", want, got)
		}
	})
}

func TestFormatCheckpointTimestampForList(t *testing.T) {
	t.Parallel()
	parsed, err := time.Parse("20060102T150405Z", "20210101T000000Z")
	if err != nil {
		t.Fatalf("parse timestamp failed: %v", err)
	}
	want := parsed.Local().Format("2006-01-02 15:04:05 MST")
	if got := formatCheckpointTimestampForList("20210101T000000Z"); got != want {
		t.Fatalf("expected formatted timestamp %q, got %q", want, got)
	}

	if got := formatCheckpointTimestampForList("not-a-timestamp"); got != "not-a-timestamp" {
		t.Fatalf("expected invalid timestamp fallback, got %q", got)
	}

	if got := formatCheckpointTimestampForList("20210101T000000Z.abc"); got != want {
		t.Fatalf("expected suffixed timestamp formatted as %q, got %q", want, got)
	}

	parsedLegacy, err := time.Parse("2006-01-02 15:04:05", "2026-06-01 12:00:00")
	if err != nil {
		t.Fatalf("parse legacy timestamp failed: %v", err)
	}
	wantLegacy := parsedLegacy.Local().Format("2006-01-02 15:04:05 MST")
	if got := formatCheckpointTimestampForList("2026-06-01 12:00:00"); got != wantLegacy {
		t.Fatalf("expected legacy formatted timestamp %q, got %q", wantLegacy, got)
	}
}

func TestParseCheckpointTimestampSupportsSuffix(t *testing.T) {
	parsed, err := parseCheckpointTimestamp("20210101T000000Z.suffix")
	if err != nil {
		t.Fatalf("parseCheckpointTimestamp failed: %v", err)
	}
	if parsed.UTC().Format("20060102T150405Z") != "20210101T000000Z" {
		t.Fatalf("expected canonical timestamp 20210101T000000Z, got %q", parsed.UTC().Format("20060102T150405Z"))
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
