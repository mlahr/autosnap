package autosnap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/cobra"
)

func requireIntegration(t *testing.T) {
	if !runIntegrationTests {
		t.Skip("run integration test with: go test -tags=integration ./...")
	}
}

func TestSnapshotRefNaming(t *testing.T) {
	t.Parallel()
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

	status, cmd = parseCheckpointMessage("feat(monitoring): introduce failing state for monitors")
	if status != "unknown" {
		t.Fatalf("expected unknown when failing appears in custom subject, got %s", status)
	}
	if cmd != "" {
		t.Fatalf("expected empty check command for custom subject, got %q", cmd)
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
			name:    "custom subject containing status word",
			message: "feat(monitoring): introduce failing state for monitors",
			want:    "feat(monitoring): introduce failing state for monitors",
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

func TestRootCommandIncludesCheckpointCommand(t *testing.T) {
	root := NewRootCommand()
	for _, command := range root.Commands() {
		if command.Name() == "checkpoint" {
			return
		}
	}
	t.Fatalf("expected root command to include checkpoint")
}

func TestRootCommandIncludesBranchCommand(t *testing.T) {
	root := NewRootCommand()
	for _, command := range root.Commands() {
		if command.Name() == "branch" {
			return
		}
	}
	t.Fatalf("expected root command to include branch")
}

func TestRootCommandIncludesDocsCommand(t *testing.T) {
	root := NewRootCommand()
	for _, command := range root.Commands() {
		if command.Name() == "docs" {
			return
		}
	}
	t.Fatalf("expected root command to include docs")
}

func TestRootCommandIncludesMarkCommand(t *testing.T) {
	root := NewRootCommand()
	for _, command := range root.Commands() {
		if command.Name() == "mark" {
			return
		}
	}
	t.Fatalf("expected root command to include mark")
}

func TestMarkCommandValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing mode", args: []string{"mark", "last"}, want: "exactly one of --bad, --good, or --unmark"},
		{name: "multiple modes", args: []string{"mark", "--bad", "--good", "last"}, want: "exactly one of --bad, --good, or --unmark"},
		{name: "reason with good", args: []string{"mark", "--good", "--reason", "fixed", "last"}, want: "--reason can only be used with --bad"},
		{name: "reason with unmark", args: []string{"mark", "--unmark", "--reason", "fixed", "last"}, want: "--reason can only be used with --bad"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
			root.AddCommand(newMarkCommand())
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestBranchCopyCommandValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing from", args: []string{"branch", "copy", "--to", "feature/next"}, want: "branch copy requires --from"},
		{name: "missing to", args: []string{"branch", "copy", "--from", "main"}, want: "branch copy requires --to"},
		{name: "invalid from", args: []string{"branch", "copy", "--from", "bad..branch", "--to", "feature/next"}, want: "invalid --from"},
		{name: "invalid to", args: []string{"branch", "copy", "--from", "main", "--to", "bad..branch"}, want: "invalid --to"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
			root.AddCommand(newBranchCommand())
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestCheckpointMarkSummaryPrefixColor(t *testing.T) {
	if got := checkpointMarkSummaryPrefix(false, checkpointMark{Mark: checkpointMarkStateBad}); got != "[bad]" {
		t.Fatalf("expected plain bad prefix, got %q", got)
	}
	if got := checkpointMarkSummaryPrefix(false, checkpointMark{Mark: checkpointMarkStateGood}); got != "[good]" {
		t.Fatalf("expected plain good prefix, got %q", got)
	}
	if got := checkpointMarkSummaryPrefix(false, checkpointMark{}); got != "" {
		t.Fatalf("expected empty unmarked prefix, got %q", got)
	}
	if got := checkpointMarkSummaryPrefix(true, checkpointMark{Mark: checkpointMarkStateBad}); !strings.Contains(got, ansiRed) || !strings.Contains(got, "[bad]") {
		t.Fatalf("expected red bad prefix, got %q", got)
	}
	if got := checkpointMarkSummaryPrefix(true, checkpointMark{Mark: checkpointMarkStateGood}); !strings.Contains(got, ansiGreen) || !strings.Contains(got, "[good]") {
		t.Fatalf("expected green good prefix, got %q", got)
	}
	if got := checkpointMarkSummaryPrefix(true, checkpointMark{}); got != "" {
		t.Fatalf("expected empty colorized unmarked prefix, got %q", got)
	}
}

func TestDocsCommandShowsInstalledDocumentationLocations(t *testing.T) {
	buf := &bytes.Buffer{}
	root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newDocsCommand())
	root.SetOut(buf)
	root.SetArgs([]string{"docs"})

	if err := root.Execute(); err != nil {
		t.Fatalf("docs command failed: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"man autosnap",
		"man autosnap-<command>",
		"/usr/share/doc/autosnap/",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected docs output to contain %q, got %q", want, out)
		}
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

func TestGitIgnoredPathsAreIgnored(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
		runner, err := newSnapshotRunnerWithWatch(context.Background(), repo, branchRef, "true", "", snapshotModeBoth, commitModeCheckpoint, watchModePoll, time.Second, time.Second, statePath)
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
		runner, err := newSnapshotRunnerWithWatch(context.Background(), repo, branchRef, "true", "", snapshotModeBoth, commitModeCheckpoint, watchModePoll, time.Second, time.Second, statePath)
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
		runner, err := newSnapshotRunnerWithWatch(context.Background(), repo, branchRef, "true", "", snapshotModeWorking, commitModeCheckpoint, watchModePoll, time.Second, time.Second, statePath)
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
		runner, err := newSnapshotRunnerWithWatch(context.Background(), repo, branchRef, "true", "", snapshotModeStaged, commitModeCheckpoint, watchModePoll, time.Second, time.Second, statePath)
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
	t.Parallel()
	repo := t.TempDir()

	cmd := newStartCommand()
	if err := cmd.Flags().Set("check", "true"); err != nil {
		t.Fatalf("set check flag failed: %v", err)
	}
	if err := cmd.Flags().Set("watch-mode", "bad"); err != nil {
		t.Fatalf("set watch-mode flag failed: %v", err)
	}
	_, _, err := resolveStartConfig(repo, cmd, "true", "", 60, snapshotModeBoth, commitModeCheckpoint, "bad", defaultPollInterval, defaultLogMaxBytes)
	if err == nil || !strings.Contains(err.Error(), "invalid --watch-mode") {
		t.Fatalf("expected invalid watch mode error, got %v", err)
	}

	cmd = newStartCommand()
	if err := cmd.Flags().Set("check", "true"); err != nil {
		t.Fatalf("set check flag failed: %v", err)
	}
	if err := cmd.Flags().Set("watch-mode", "poll"); err != nil {
		t.Fatalf("set watch-mode flag failed: %v", err)
	}
	if err := cmd.Flags().Set("poll-interval", "0s"); err != nil {
		t.Fatalf("set poll-interval flag failed: %v", err)
	}
	_, _, err = resolveStartConfig(repo, cmd, "true", "", 60, snapshotModeBoth, commitModeCheckpoint, watchModePoll, 0, defaultLogMaxBytes)
	if err == nil || !strings.Contains(err.Error(), "--poll-interval must be greater than 0") {
		t.Fatalf("expected invalid poll interval error, got %v", err)
	}
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
commit_mode = "sync"
msg_source_cmd = "printf msg"
note_command = "printf note"
note_ref = "refs/notes/diffcog"
log_max_bytes = 2048

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
	if cfg.Check != "go test ./..." || cfg.IdleSeconds != 15 || cfg.SnapshotMode != snapshotModeStaged || cfg.CommitMode != commitModeSync || cfg.MsgSourceCmd != "printf msg" || cfg.NoteCommand != "printf note" || cfg.NoteRef != "refs/notes/diffcog" || cfg.LogMaxBytes != 2048 || cfg.Watch.Mode != watchModeAuto || cfg.Watch.PollInterval != 2*time.Second {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestResolveStartConfigPrefersFlagsOverConfig(t *testing.T) {
	repo := t.TempDir()
	raw := []byte(`check = "go test ./..."
idle_seconds = 15
snapshot_mode = "staged"
commit_mode = "direct"
log_max_bytes = 4096

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
	if err := cmd.Flags().Set("commit-mode", "checkpoint"); err != nil {
		t.Fatalf("set commit-mode flag failed: %v", err)
	}
	if err := cmd.Flags().Set("log-max-bytes", "8192"); err != nil {
		t.Fatalf("set log-max-bytes flag failed: %v", err)
	}
	if err := cmd.Flags().Set("note-command", "printf note"); err != nil {
		t.Fatalf("set note-command flag failed: %v", err)
	}
	if err := cmd.Flags().Set("note-ref", "refs/notes/diffcog"); err != nil {
		t.Fatalf("set note-ref flag failed: %v", err)
	}

	cfg, found, err := resolveStartConfig(repo, cmd, "make test", "", 30, snapshotModeBoth, commitModeCheckpoint, watchModeAuto, defaultPollInterval, 8192)
	if err != nil {
		t.Fatalf("resolve config failed: %v", err)
	}
	if !found {
		t.Fatalf("expected config to be found")
	}
	if cfg.Check != "make test" || cfg.IdleSeconds != 30 || cfg.CommitMode != commitModeCheckpoint || cfg.Watch.Mode != watchModeAuto || cfg.LogMaxBytes != 8192 || cfg.NoteCommand != "printf note" || cfg.NoteRef != "refs/notes/diffcog" {
		t.Fatalf("expected flags to override config, got %+v", cfg)
	}
	if cfg.SnapshotMode != snapshotModeStaged || cfg.Watch.PollInterval != 2*time.Second {
		t.Fatalf("expected config values to remain for unset flags, got %+v", cfg)
	}
}

func TestResolveStartConfigValidatesNoteConfig(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(autosnapConfigPath(repo), []byte("check = \"true\"\nnote_command = \"printf note\"\n"), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	_, _, err := resolveStartConfig(repo, newStartCommand(), "", "", 60, snapshotModeBoth, commitModeCheckpoint, watchModeRecursive, defaultPollInterval, defaultLogMaxBytes)
	if err == nil || !strings.Contains(err.Error(), "note_ref is required when note_command is set") {
		t.Fatalf("expected missing note_ref error, got %v", err)
	}

	if err := os.WriteFile(autosnapConfigPath(repo), []byte("check = \"true\"\nnote_ref = \"refs/notes/diffcog\"\n"), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	_, _, err = resolveStartConfig(repo, newStartCommand(), "", "", 60, snapshotModeBoth, commitModeCheckpoint, watchModeRecursive, defaultPollInterval, defaultLogMaxBytes)
	if err == nil || !strings.Contains(err.Error(), "note_command is required when note_ref is set") {
		t.Fatalf("expected missing note_command error, got %v", err)
	}
}

func TestResolveStartConfigRejectsUnsafeDirectSnapshotMode(t *testing.T) {
	repo := t.TempDir()
	raw := []byte(`check = "go test ./..."
snapshot_mode = "staged"
commit_mode = "direct"
`)
	if err := os.WriteFile(autosnapConfigPath(repo), raw, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	_, _, err := resolveStartConfig(repo, newStartCommand(), "", "", 60, snapshotModeBoth, commitModeCheckpoint, watchModeRecursive, defaultPollInterval, defaultLogMaxBytes)
	if err == nil {
		t.Fatalf("expected unsafe direct snapshot mode to fail")
	}
	if !strings.Contains(err.Error(), "commit_mode direct requires snapshot_mode both") {
		t.Fatalf("expected direct snapshot mode error, got %v", err)
	}
}

func TestResolveStartConfigRejectsUnsafeSyncSnapshotMode(t *testing.T) {
	repo := t.TempDir()
	raw := []byte(`check = "go test ./..."
snapshot_mode = "staged"
commit_mode = "sync"
`)
	if err := os.WriteFile(autosnapConfigPath(repo), raw, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	_, _, err := resolveStartConfig(repo, newStartCommand(), "", "", 60, snapshotModeBoth, commitModeCheckpoint, watchModeRecursive, defaultPollInterval, defaultLogMaxBytes)
	if err == nil {
		t.Fatalf("expected unsafe sync snapshot mode to fail")
	}
	if !strings.Contains(err.Error(), "commit_mode sync requires snapshot_mode both") {
		t.Fatalf("expected sync snapshot mode error, got %v", err)
	}
}

func TestResolveStartConfigRejectsInvalidLogMaxBytes(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(autosnapConfigPath(repo), []byte("check = \"true\"\nlog_max_bytes = 0\n"), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	_, _, err := resolveStartConfig(repo, newStartCommand(), "", "", 60, snapshotModeBoth, commitModeCheckpoint, watchModeRecursive, defaultPollInterval, defaultLogMaxBytes)
	if err == nil || !strings.Contains(err.Error(), "log_max_bytes must be greater than 0") {
		t.Fatalf("expected invalid config log_max_bytes error, got %v", err)
	}

	cmd := newStartCommand()
	if err := cmd.Flags().Set("log-max-bytes", "0"); err != nil {
		t.Fatalf("set log-max-bytes flag failed: %v", err)
	}
	_, _, err = resolveStartConfig(repo, cmd, "", "", 60, snapshotModeBoth, commitModeCheckpoint, watchModeRecursive, defaultPollInterval, 0)
	if err == nil || !strings.Contains(err.Error(), "--log-max-bytes must be greater than 0") {
		t.Fatalf("expected invalid flag log-max-bytes error, got %v", err)
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
note_command = "printf config-note"
note_ref = "refs/notes/config"

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
		NoteCommand:     "printf run-note",
		NoteRef:         "refs/notes/run",
		IdleSeconds:     45,
		SnapshotMode:    snapshotModeBoth,
		CommitMode:      commitModeDirect,
		WatchMode:       watchModePoll,
		PollInterval:    2 * time.Second,
		LogMaxBytes:     4096,
	}
	cfg, err := resolveRestartConfig(repo, newRestartCommand(), runState, true, "", "", 60, snapshotModeBoth, commitModeCheckpoint, watchModeRecursive, defaultPollInterval, defaultLogMaxBytes)
	if err != nil {
		t.Fatalf("resolve restart config failed: %v", err)
	}
	if cfg.Check != "make verify" || cfg.MsgSourceCmd != "" || cfg.NoteCommand != "printf run-note" || cfg.NoteRef != "refs/notes/run" || cfg.IdleSeconds != 45 || cfg.SnapshotMode != snapshotModeBoth || cfg.CommitMode != commitModeDirect || cfg.Watch.Mode != watchModePoll || cfg.Watch.PollInterval != 2*time.Second || cfg.LogMaxBytes != 4096 {
		t.Fatalf("expected active run state to win, got %+v", cfg)
	}
}

func TestResolveRestartConfigFlagsOverrideActiveRunState(t *testing.T) {
	repo := t.TempDir()
	runState := autosnapRunState{
		CheckCommand:    "make verify",
		MsgSourceCmd:    "printf old",
		MsgSourceCmdSet: true,
		NoteCommand:     "printf old-note",
		NoteRef:         "refs/notes/old",
		IdleSeconds:     45,
		SnapshotMode:    snapshotModeStaged,
		CommitMode:      commitModeDirect,
		WatchMode:       watchModePoll,
		PollInterval:    2 * time.Second,
		LogMaxBytes:     4096,
	}
	cmd := newRestartCommand()
	if err := cmd.Flags().Set("check", "npm test"); err != nil {
		t.Fatalf("set check flag failed: %v", err)
	}
	if err := cmd.Flags().Set("msg-source-cmd", "printf new"); err != nil {
		t.Fatalf("set msg-source-cmd flag failed: %v", err)
	}
	if err := cmd.Flags().Set("note-command", "printf new-note"); err != nil {
		t.Fatalf("set note-command flag failed: %v", err)
	}
	if err := cmd.Flags().Set("note-ref", "refs/notes/new"); err != nil {
		t.Fatalf("set note-ref flag failed: %v", err)
	}
	if err := cmd.Flags().Set("idle", "30"); err != nil {
		t.Fatalf("set idle flag failed: %v", err)
	}
	if err := cmd.Flags().Set("watch-mode", "auto"); err != nil {
		t.Fatalf("set watch-mode flag failed: %v", err)
	}
	if err := cmd.Flags().Set("commit-mode", "checkpoint"); err != nil {
		t.Fatalf("set commit-mode flag failed: %v", err)
	}
	if err := cmd.Flags().Set("poll-interval", "3s"); err != nil {
		t.Fatalf("set poll-interval flag failed: %v", err)
	}
	if err := cmd.Flags().Set("log-max-bytes", "8192"); err != nil {
		t.Fatalf("set log-max-bytes flag failed: %v", err)
	}

	cfg, err := resolveRestartConfig(repo, cmd, runState, true, "npm test", "printf new", 30, snapshotModeBoth, commitModeCheckpoint, watchModeAuto, 3*time.Second, 8192)
	if err != nil {
		t.Fatalf("resolve restart config failed: %v", err)
	}
	if cfg.Check != "npm test" || cfg.MsgSourceCmd != "printf new" || cfg.NoteCommand != "printf new-note" || cfg.NoteRef != "refs/notes/new" || cfg.IdleSeconds != 30 || cfg.CommitMode != commitModeCheckpoint || cfg.Watch.Mode != watchModeAuto || cfg.Watch.PollInterval != 3*time.Second || cfg.LogMaxBytes != 8192 {
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
snapshot_mode = "both"
commit_mode = "direct"
msg_source_cmd = "printf config"
log_max_bytes = 4096

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
	cfg, err := resolveRestartConfig(repo, newRestartCommand(), runState, true, "", "", 60, snapshotModeBoth, commitModeCheckpoint, watchModeRecursive, defaultPollInterval, defaultLogMaxBytes)
	if err != nil {
		t.Fatalf("resolve restart config failed: %v", err)
	}
	if cfg.Check != "make verify" || cfg.IdleSeconds != 45 {
		t.Fatalf("expected present legacy run values to win, got %+v", cfg)
	}
	if cfg.MsgSourceCmd != "printf config" || cfg.SnapshotMode != snapshotModeBoth || cfg.CommitMode != commitModeDirect || cfg.Watch.Mode != watchModeAuto || cfg.Watch.PollInterval != 4*time.Second || cfg.LogMaxBytes != 4096 {
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

	cfg, err := resolveRestartConfig(repo, newRestartCommand(), autosnapRunState{}, false, "", "", 60, snapshotModeBoth, commitModeCheckpoint, watchModeRecursive, defaultPollInterval, defaultLogMaxBytes)
	if err != nil {
		t.Fatalf("resolve restart config failed: %v", err)
	}
	if cfg.Check != "go test ./..." || cfg.IdleSeconds != 15 || cfg.Watch.Mode != watchModePoll || cfg.Watch.PollInterval != defaultPollInterval {
		t.Fatalf("expected inactive restart to use start config resolution, got %+v", cfg)
	}
}

func TestConfigInitAndShowCommands(t *testing.T) {
	repo := t.TempDir()

	buf := &bytes.Buffer{}
	if err := writeDefaultAutosnapConfig(repo, buf, false); err != nil {
		t.Fatalf("config init failed: %v", err)
	}
	if _, err := os.Stat(autosnapConfigPath(repo)); err != nil {
		t.Fatalf("expected config file to exist: %v", err)
	}

	if err := writeDefaultAutosnapConfig(repo, io.Discard, false); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected init to refuse overwrite, got %v", err)
	}

	buf.Reset()
	if err := writeResolvedAutosnapConfig(repo, buf); err != nil {
		t.Fatalf("config show failed: %v", err)
	}
	expectedConfigPath := autosnapConfigPath(repo)
	out := buf.String()
	for _, want := range []string{
		"path: " + expectedConfigPath,
		"exists: true",
		"check: make test",
		"commit_mode: checkpoint",
		"note_command: ",
		"note_ref: ",
		"log_max_bytes: 10485760",
		"watch.mode: recursive",
		"watch.poll_interval: 5s",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected config show output to contain %q, got %q", want, out)
		}
	}
}

func TestStartCommandAcceptsConfigCheck(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(autosnapConfigPath(repo), []byte("check = \"true\"\nidle_seconds = 1\n"), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	cmd := newStartCommand()
	if err := cmd.Flags().Set("watch-mode", "bad"); err != nil {
		t.Fatalf("set watch-mode flag failed: %v", err)
	}
	_, _, err := resolveStartConfig(repo, cmd, "", "", 60, snapshotModeBoth, commitModeCheckpoint, "bad", defaultPollInterval, defaultLogMaxBytes)
	if err == nil || !strings.Contains(err.Error(), "invalid --watch-mode") {
		t.Fatalf("expected invalid watch mode error after config-provided check, got %v", err)
	}
}

func TestStartDetachedArgsForwardWatchOptions(t *testing.T) {
	args := startDetachedArgs("/bin/autosnap", "make build", "printf msg", "printf note", "refs/notes/diffcog", 30, snapshotModeBoth, commitModeCheckpoint, watchModeAuto, 2*time.Second, 4096, "token")
	joined := strings.Join(args, "\n")
	for _, want := range []string{
		"--watch-mode\nauto",
		"--poll-interval\n2s",
		"--commit-mode\ncheckpoint",
		"--log-max-bytes\n4096",
		"--msg-source-cmd\nprintf msg",
		"--note-command\nprintf note",
		"--note-ref\nrefs/notes/diffcog",
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

func TestSnapshotEventOperations(t *testing.T) {
	if got := snapshotEventOperations(fsnotify.Event{Op: fsnotify.Create}); got != "CREATE" {
		t.Fatalf("expected CREATE, got %q", got)
	}
	if got := snapshotEventOperations(fsnotify.Event{Op: fsnotify.Write}); got != "WRITE" {
		t.Fatalf("expected WRITE, got %q", got)
	}
	if got := snapshotEventOperations(fsnotify.Event{Op: fsnotify.Create | fsnotify.Write}); got != "CREATE,WRITE" {
		t.Fatalf("expected CREATE,WRITE, got %q", got)
	}
	if got := snapshotEventOperations(fsnotify.Event{Op: fsnotify.Chmod}); got != "" {
		t.Fatalf("expected CHMOD to be ignored, got %q", got)
	}
	if got := snapshotEventOperations(fsnotify.Event{Op: fsnotify.Write | fsnotify.Chmod}); got != "WRITE" {
		t.Fatalf("expected WRITE with CHMOD ignored, got %q", got)
	}
	if got := snapshotEventOperations(fsnotify.Event{Op: 0}); got != "" {
		t.Fatalf("expected empty operations, got %q", got)
	}
}

func TestCommitModeHelpers(t *testing.T) {
	if mode, err := normalizeCommitMode(""); err != nil || mode != commitModeCheckpoint {
		t.Fatalf("expected empty commit mode to normalize to checkpoint, got %q err=%v", mode, err)
	}
	if mode, err := normalizeCommitMode(commitModeDirect); err != nil || mode != commitModeDirect {
		t.Fatalf("expected direct commit mode to normalize, got %q err=%v", mode, err)
	}
	if mode, err := normalizeCommitMode(commitModeSync); err != nil || mode != commitModeSync {
		t.Fatalf("expected sync commit mode to normalize, got %q err=%v", mode, err)
	}
	if _, err := normalizeCommitMode("bad"); err == nil {
		t.Fatalf("expected invalid commit mode error")
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
	t.Parallel()
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
		originalNow := currentTimestampFn
		timestamps := []string{"20260101T120000Z", "20260101T120001Z"}
		i := 0
		currentTimestampFn = func() string {
			value := timestamps[i]
			i++
			return value
		}
		t.Cleanup(func() { currentTimestampFn = originalNow })

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("changed 1\n"), 0o644); err != nil {
			t.Fatalf("write first checkpoint failed: %v", err)
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

func TestCreateCheckpointAllocatesUniqueRefsForSameTimestamp(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		originalNow := currentTimestampFn
		fixedTs := "20260101T120000Z"
		currentTimestampFn = func() string { return fixedTs }
		defer func() {
			currentTimestampFn = originalNow
		}()

		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("first\n"), 0o644); err != nil {
			t.Fatalf("write first checkpoint failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		ref1, commit1, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "")
		if err != nil {
			t.Fatalf("create first checkpoint failed: %v", err)
		}
		if path.Base(ref1) != fixedTs {
			t.Fatalf("expected first checkpoint ref to be timestamp %q, got %q", fixedTs, ref1)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("updated"), 0o644); err != nil {
			t.Fatalf("update file failed: %v", err)
		}
		tree2, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree for second checkpoint failed: %v", err)
		}
		ref2, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree2, "")
		if err != nil {
			t.Fatalf("create second checkpoint failed: %v", err)
		}
		if ref1 == ref2 {
			t.Fatalf("expected unique checkpoint refs, got duplicate %q", ref2)
		}
		if !strings.Contains(path.Base(ref2), fixedTs+".") {
			t.Fatalf("expected suffixed timestamp ref for colliding checkpoint, got %q", ref2)
		}
		if !strings.HasPrefix(path.Base(ref2), fixedTs+".") {
			t.Fatalf("expected second checkpoint to share timestamp %q, got %q", fixedTs, ref2)
		}

		checkpoints, err := listCheckpointRefsForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("listCheckpointRefsForBranch failed: %v", err)
		}
		if len(checkpoints) != 2 {
			t.Fatalf("expected 2 checkpoints, got %d", len(checkpoints))
		}
		for _, checkpoint := range checkpoints {
			if checkpoint.Timestamp != fixedTs {
				t.Fatalf("expected canonical timestamp %q, got %q for %s", fixedTs, checkpoint.Timestamp, checkpoint.Ref)
			}
		}

		secondCommit := runGitOutput(t, repo, "rev-parse", ref2)
		if commit1 == secondCommit {
			t.Fatalf("expected second checkpoint to point to different commit than first")
		}
		runGitOutput(t, repo, "rev-parse", ref1)
		runGitOutput(t, repo, "rev-parse", ref2)
	})
}

func TestListAndParseCheckpointRefsPreserveLegacyTimestampFormat(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		timestamp := "20200101T000000Z"
		ref := createAutosnapTestRef(t, repo, branchRef, timestamp)

		checkpoints, err := listCheckpointRefsForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("listCheckpointRefsForBranch failed: %v", err)
		}
		if len(checkpoints) != 1 {
			t.Fatalf("expected one checkpoint, got %d", len(checkpoints))
		}
		if checkpoints[0].Ref != ref {
			t.Fatalf("expected listed ref %s, got %s", ref, checkpoints[0].Ref)
		}
		if checkpoints[0].Timestamp != timestamp {
			t.Fatalf("expected canonical timestamp %q, got %q", timestamp, checkpoints[0].Timestamp)
		}

		output := formatCheckpointTimestampForList(checkpoints[0].Timestamp)
		formatted, _ := time.Parse("20060102T150405Z", timestamp)
		expected := formatted.Local().Format("2006-01-02 15:04:05 MST")
		if output != expected {
			t.Fatalf("expected formatted %q, got %q", expected, output)
		}
	})
}

func TestRunCheckUsesCurrentBranchOnEachRun(t *testing.T) {
	t.Parallel()
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

		if err := os.WriteFile(filepath.Join(repo, "main.txt"), []byte("main checkpoint"), 0o644); err != nil {
			t.Fatalf("write main checkpoint file failed: %v", err)
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

func TestRunCheckDirectCommitCreatesBranchCommitAndCleansWorktree(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ctx := context.Background()
		repoRoot, _, branchRef, err := detectRepository(ctx)
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		initialHead := runGitOutput(t, repoRoot, "rev-parse", "HEAD")

		statePath, err := stateFilePath(repoRoot)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}
		msgSourceCmd := `printf 'direct autosnap commit

base:%s
prev:%s
' "$AUTOSNAP_DIFF_BASE" "$AUTOSNAP_PREVIOUS_CHECKPOINT_REF"`
		runner, err := newSnapshotRunnerWithWatch(ctx, repoRoot, branchRef, "true", msgSourceCmd, snapshotModeBoth, commitModeDirect, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunnerWithWatch failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repoRoot, "file.txt"), []byte("direct change"), 0o644); err != nil {
			t.Fatalf("write tracked file failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repoRoot, "new.txt"), []byte("new file"), 0o644); err != nil {
			t.Fatalf("write untracked file failed: %v", err)
		}

		runner.runCheck()

		newHead := runGitOutput(t, repoRoot, "rev-parse", "HEAD")
		if newHead == initialHead {
			t.Fatalf("expected direct mode to advance HEAD")
		}
		if runner.state.LastCheckpointRef != newHead {
			t.Fatalf("expected state to record direct commit %s, got %s", newHead, runner.state.LastCheckpointRef)
		}
		if runner.state.LastCheckpointAt == "" {
			t.Fatalf("expected state to record direct commit timestamp")
		}
		if status := runGitOutput(t, repoRoot, "status", "--porcelain"); status != "" {
			t.Fatalf("expected direct commit to leave clean worktree, got %q", status)
		}
		if got := runGitOutput(t, repoRoot, "show", "HEAD:file.txt"); got != "direct change" {
			t.Fatalf("expected tracked change in direct commit, got %q", got)
		}
		if got := runGitOutput(t, repoRoot, "show", "HEAD:new.txt"); got != "new file" {
			t.Fatalf("expected untracked file in direct commit, got %q", got)
		}
		message := runGitOutput(t, repoRoot, "log", "-1", "--pretty=%B")
		if !strings.Contains(message, "direct autosnap commit") || !strings.Contains(message, "base:"+initialHead) || !strings.Contains(message, "prev:") {
			t.Fatalf("expected direct commit message source output, got %q", message)
		}

		ref, _, _, err := getLatestCheckpointForBranch(ctx, repoRoot, branchRef)
		if err != nil {
			t.Fatalf("getLatestCheckpointForBranch failed: %v", err)
		}
		if ref != "" {
			t.Fatalf("expected direct mode not to create checkpoint refs, got %s", ref)
		}
	})
}

func TestRunCheckDirectCommitSkipsMessageSourceWhenTreeMatchesHead(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ctx := context.Background()
		repoRoot, _, branchRef, err := detectRepository(ctx)
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		initialHead := runGitOutput(t, repoRoot, "rev-parse", "HEAD")

		statePath, err := stateFilePath(repoRoot)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}
		checkSentinelPath := filepath.Join(repoRoot, "check-ran")
		msgSourceSentinelPath := filepath.Join(repoRoot, "msg-source-ran")
		checkCmd := "printf ran > check-ran"
		msgSourceCmd := "printf ran > msg-source-ran && printf message"
		runner, err := newSnapshotRunnerWithWatch(ctx, repoRoot, branchRef, checkCmd, msgSourceCmd, snapshotModeBoth, commitModeDirect, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunnerWithWatch failed: %v", err)
		}

		runner.runCheck()

		if _, err := os.Stat(checkSentinelPath); !os.IsNotExist(err) {
			t.Fatalf("expected check command not to run, stat err=%v", err)
		}
		if _, err := os.Stat(msgSourceSentinelPath); !os.IsNotExist(err) {
			t.Fatalf("expected msg-source-cmd not to run, stat err=%v", err)
		}
		if got := runGitOutput(t, repoRoot, "rev-parse", "HEAD"); got != initialHead {
			t.Fatalf("expected direct mode to leave HEAD unchanged, got %s want %s", got, initialHead)
		}
	})
}

func TestRunCheckCheckpointSkipsMessageSourceWhenTreeMatchesHead(t *testing.T) {
	t.Parallel()
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
		checkSentinelPath := filepath.Join(repoRoot, "check-ran")
		msgSourceSentinelPath := filepath.Join(repoRoot, "msg-source-ran")
		checkCmd := "printf ran > check-ran"
		msgSourceCmd := "printf ran > msg-source-ran && printf message"
		runner, err := newSnapshotRunnerWithWatch(ctx, repoRoot, branchRef, checkCmd, msgSourceCmd, snapshotModeBoth, commitModeCheckpoint, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunnerWithWatch failed: %v", err)
		}

		runner.runCheck()

		if _, err := os.Stat(checkSentinelPath); !os.IsNotExist(err) {
			t.Fatalf("expected check command not to run, stat err=%v", err)
		}
		if _, err := os.Stat(msgSourceSentinelPath); !os.IsNotExist(err) {
			t.Fatalf("expected msg-source-cmd not to run, stat err=%v", err)
		}
		if runner.state.LastCheckpointRef != "" {
			t.Fatalf("expected checkpoint mode not to record checkpoint, got %s", runner.state.LastCheckpointRef)
		}
	})
}

func TestRunCheckCheckpointSkipsCheckWhenTreeMatchesPreviousCheckpoint(t *testing.T) {
	t.Parallel()
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
		runner, err := newSnapshotRunnerWithWatch(ctx, repoRoot, branchRef, "true", "", snapshotModeBoth, commitModeCheckpoint, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunnerWithWatch failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repoRoot, "file.txt"), []byte("checkpointed change"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
		runner.runCheck()

		if runner.state.LastCheckpointTree == "" {
			t.Fatalf("expected initial checkpoint to record tree")
		}

		checkSentinelPath := filepath.Join(repoRoot, "check-ran")
		runner.checkCmd = "printf ran > check-ran"
		runner.runCheck()

		if _, err := os.Stat(checkSentinelPath); !os.IsNotExist(err) {
			t.Fatalf("expected check command not to run, stat err=%v", err)
		}
	})
}

func TestRunCheckDirectCommitComparesAgainstHeadNotCheckpoint(t *testing.T) {
	t.Parallel()
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
		runner, err := newSnapshotRunnerWithWatch(ctx, repoRoot, branchRef, "true", "", snapshotModeBoth, commitModeCheckpoint, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("new checkpoint runner failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repoRoot, "file.txt"), []byte("same tree as checkpoint"), 0o644); err != nil {
			t.Fatalf("write checkpoint content failed: %v", err)
		}
		runner.runCheck()
		checkpointTree := runner.state.LastCheckpointTree
		checkpointRef := runner.state.LastCheckpointRef
		if checkpointTree == "" || checkpointRef == "" {
			t.Fatalf("expected checkpoint state to be populated")
		}

		directRunner, err := newSnapshotRunnerWithWatch(ctx, repoRoot, branchRef, "true", "", snapshotModeBoth, commitModeDirect, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("new direct runner failed: %v", err)
		}
		initialHead := runGitOutput(t, repoRoot, "rev-parse", "HEAD")

		directRunner.runCheck()

		newHead := runGitOutput(t, repoRoot, "rev-parse", "HEAD")
		if newHead == initialHead {
			t.Fatalf("expected direct mode to commit when worktree matches previous checkpoint but differs from HEAD")
		}
		if directRunner.state.LastCheckpointRef != newHead {
			t.Fatalf("expected direct mode state to record new HEAD %s, got %s", newHead, directRunner.state.LastCheckpointRef)
		}
		if status := runGitOutput(t, repoRoot, "status", "--porcelain"); status != "" {
			t.Fatalf("expected direct commit to leave clean worktree, got %q", status)
		}
		if got := runGitOutput(t, repoRoot, "show", "HEAD:file.txt"); got != "same tree as checkpoint" {
			t.Fatalf("expected direct commit to capture checkpoint-matching tree, got %q", got)
		}
	})
}

func TestRunCheckDirectCommitSkipsFailedCheck(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ctx := context.Background()
		repoRoot, _, branchRef, err := detectRepository(ctx)
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		initialHead := runGitOutput(t, repoRoot, "rev-parse", "HEAD")

		statePath, err := stateFilePath(repoRoot)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}
		runner, err := newSnapshotRunnerWithWatch(ctx, repoRoot, branchRef, "false", "", snapshotModeBoth, commitModeDirect, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunnerWithWatch failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repoRoot, "file.txt"), []byte("failed check change"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
		runner.runCheck()

		if head := runGitOutput(t, repoRoot, "rev-parse", "HEAD"); head != initialHead {
			t.Fatalf("expected failed check not to advance HEAD, got %s want %s", head, initialHead)
		}
		if runner.state.LastCheckpointRef != "" {
			t.Fatalf("expected failed check not to record commit, got %s", runner.state.LastCheckpointRef)
		}
		if status := runGitOutput(t, repoRoot, "status", "--porcelain"); status == "" {
			t.Fatalf("expected failed check to leave working-tree changes")
		}
	})
}

func TestRunCheckDirectCommitRejectsDetachedHead(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ctx := context.Background()
		initialHead := runGitOutput(t, repo, "rev-parse", "HEAD")
		runGit(t, repo, "checkout", "--detach", "HEAD")
		repoRoot, _, branchRef, err := detectRepository(ctx)
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		statePath, err := stateFilePath(repoRoot)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}
		runner, err := newSnapshotRunnerWithWatch(ctx, repoRoot, branchRef, "true", "", snapshotModeBoth, commitModeDirect, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunnerWithWatch failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repoRoot, "file.txt"), []byte("detached change"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
		runner.runCheck()

		if head := runGitOutput(t, repoRoot, "rev-parse", "HEAD"); head != initialHead {
			t.Fatalf("expected detached direct mode not to advance HEAD, got %s want %s", head, initialHead)
		}
		if runner.state.LastCheckpointRef != "" {
			t.Fatalf("expected detached direct mode not to record commit, got %s", runner.state.LastCheckpointRef)
		}
		if status := runGitOutput(t, repoRoot, "status", "--porcelain"); status == "" {
			t.Fatalf("expected detached direct mode to leave working-tree changes")
		}
	})
}

func TestRunCheckDirectCommitDefersWhenWorktreeChangesDuringCheck(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ctx := context.Background()
		repoRoot, _, branchRef, err := detectRepository(ctx)
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		initialHead := runGitOutput(t, repoRoot, "rev-parse", "HEAD")

		statePath, err := stateFilePath(repoRoot)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}
		runner, err := newSnapshotRunnerWithWatch(ctx, repoRoot, branchRef, "true", "printf changed > late.txt && printf message", snapshotModeBoth, commitModeSync, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("new sync runner failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repoRoot, "file.txt"), []byte("initial direct change"), 0o644); err != nil {
			t.Fatalf("write tracked file failed: %v", err)
		}

		runner.runCheck()

		if head := runGitOutput(t, repoRoot, "rev-parse", "HEAD"); head != initialHead {
			t.Fatalf("expected stale direct snapshot to defer commit, got HEAD %s want %s", head, initialHead)
		}
		if runner.state.LastCheckpointRef != "" {
			t.Fatalf("expected deferred commit not to record checkpoint ref, got %s", runner.state.LastCheckpointRef)
		}
		status := runGitOutput(t, repoRoot, "status", "--porcelain")
		if !strings.Contains(status, "M file.txt") || !strings.Contains(status, "?? late.txt") {
			t.Fatalf("expected original and late changes to remain pending, got %q", status)
		}
	})
}

func TestRunCheckSyncCommitPullsRebasesAndPushes(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	remoteRoot := t.TempDir()
	remote := filepath.Join(remoteRoot, "origin.git")
	runGit(t, remoteRoot, "init", "--bare", remote)
	runGit(t, repo, "remote", "add", "origin", remote)
	runGit(t, repo, "push", "-u", "origin", "HEAD")

	other := filepath.Join(t.TempDir(), "other")
	runGit(t, t.TempDir(), "clone", remote, other)
	runGit(t, other, "config", "user.name", "Autosnap Test")
	runGit(t, other, "config", "user.email", "test@autosnap.local")
	if err := os.WriteFile(filepath.Join(other, "upstream.txt"), []byte("upstream change"), 0o644); err != nil {
		t.Fatalf("write upstream file failed: %v", err)
	}
	runGit(t, other, "add", "upstream.txt")
	runGit(t, other, "commit", "-m", "upstream change")
	runGit(t, other, "push")

	withWorkingDir(t, repo, func() {
		ctx := context.Background()
		repoRoot, _, branchRef, err := detectRepository(ctx)
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		initialHead := runGitOutput(t, repoRoot, "rev-parse", "HEAD")

		statePath, err := stateFilePath(repoRoot)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}
		runner, err := newSnapshotRunnerWithWatch(ctx, repoRoot, branchRef, "true", "", snapshotModeBoth, commitModeSync, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("new sync runner failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repoRoot, "file.txt"), []byte("sync change"), 0o644); err != nil {
			t.Fatalf("write tracked file failed: %v", err)
		}

		runner.runCheck()

		newHead := runGitOutput(t, repoRoot, "rev-parse", "HEAD")
		if newHead == initialHead {
			t.Fatalf("expected sync mode to advance HEAD")
		}
		if runner.state.LastCheckpointRef != newHead {
			t.Fatalf("expected state to record synced HEAD %s, got %s", newHead, runner.state.LastCheckpointRef)
		}
		if status := runGitOutput(t, repoRoot, "status", "--porcelain"); status != "" {
			t.Fatalf("expected sync commit to leave clean worktree, got %q", status)
		}
		if got := runGitOutput(t, repoRoot, "show", "HEAD:file.txt"); got != "sync change" {
			t.Fatalf("expected local change in synced commit, got %q", got)
		}
		if got := runGitOutput(t, repoRoot, "show", "HEAD:upstream.txt"); got != "upstream change" {
			t.Fatalf("expected pulled upstream change, got %q", got)
		}

		remoteHead := runGitOutput(t, repoRoot, "ls-remote", "origin", "refs/heads/"+branchRef)
		if !strings.HasPrefix(remoteHead, newHead+"\t") {
			t.Fatalf("expected remote branch to be pushed to %s, got %q", newHead, remoteHead)
		}
	})
}

func TestRunCheckSyncCommitAbortsRebaseConflictAndKeepsLocalCommit(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	remoteRoot := t.TempDir()
	remote := filepath.Join(remoteRoot, "origin.git")
	runGit(t, remoteRoot, "init", "--bare", remote)
	runGit(t, repo, "remote", "add", "origin", remote)
	runGit(t, repo, "push", "-u", "origin", "HEAD")

	other := filepath.Join(t.TempDir(), "other")
	runGit(t, t.TempDir(), "clone", remote, other)
	runGit(t, other, "config", "user.name", "Autosnap Test")
	runGit(t, other, "config", "user.email", "test@autosnap.local")
	if err := os.WriteFile(filepath.Join(other, "file.txt"), []byte("upstream conflict"), 0o644); err != nil {
		t.Fatalf("write upstream file failed: %v", err)
	}
	runGit(t, other, "add", "file.txt")
	runGit(t, other, "commit", "-m", "upstream conflict")
	runGit(t, other, "push")

	withWorkingDir(t, repo, func() {
		ctx := context.Background()
		repoRoot, _, branchRef, err := detectRepository(ctx)
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		initialHead := runGitOutput(t, repoRoot, "rev-parse", "HEAD")

		statePath, err := stateFilePath(repoRoot)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}
		runner, err := newSnapshotRunnerWithWatch(ctx, repoRoot, branchRef, "true", "", snapshotModeBoth, commitModeSync, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("new sync runner failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repoRoot, "file.txt"), []byte("local conflict"), 0o644); err != nil {
			t.Fatalf("write local file failed: %v", err)
		}

		runner.runCheck()

		localHead := runGitOutput(t, repoRoot, "rev-parse", "HEAD")
		if localHead == initialHead {
			t.Fatalf("expected sync mode to create local commit before failed rebase")
		}
		if runner.state.LastCheckpointRef != localHead {
			t.Fatalf("expected state to record local HEAD %s, got %s", localHead, runner.state.LastCheckpointRef)
		}
		if status := runGitOutput(t, repoRoot, "status", "--porcelain"); status != "" {
			t.Fatalf("expected failed rebase to abort back to clean worktree, got %q", status)
		}
		if got := runGitOutput(t, repoRoot, "show", "HEAD:file.txt"); got != "local conflict" {
			t.Fatalf("expected local commit content after abort, got %q", got)
		}
		remoteHead := runGitOutput(t, repoRoot, "rev-parse", "origin/"+branchRef)
		if remoteHead == localHead {
			t.Fatalf("expected failed sync not to push local commit")
		}
	})
}

func TestRunCheckSyncCommitKeepsLocalCommitWithoutUpstream(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ctx := context.Background()
		repoRoot, _, branchRef, err := detectRepository(ctx)
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		initialHead := runGitOutput(t, repoRoot, "rev-parse", "HEAD")

		statePath, err := stateFilePath(repoRoot)
		if err != nil {
			t.Fatalf("stateFilePath failed: %v", err)
		}
		runner, err := newSnapshotRunnerWithWatch(ctx, repoRoot, branchRef, "true", "", snapshotModeBoth, commitModeSync, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("new sync runner failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repoRoot, "file.txt"), []byte("local sync change"), 0o644); err != nil {
			t.Fatalf("write tracked file failed: %v", err)
		}

		runner.runCheck()

		newHead := runGitOutput(t, repoRoot, "rev-parse", "HEAD")
		if newHead == initialHead {
			t.Fatalf("expected sync mode to keep local commit even without upstream")
		}
		if runner.state.LastCheckpointRef != newHead {
			t.Fatalf("expected state to record local HEAD %s, got %s", newHead, runner.state.LastCheckpointRef)
		}
		if status := runGitOutput(t, repoRoot, "status", "--porcelain"); status != "" {
			t.Fatalf("expected failed sync to leave clean worktree after local commit, got %q", status)
		}
		if got := runGitOutput(t, repoRoot, "show", "HEAD:file.txt"); got != "local sync change" {
			t.Fatalf("expected local change to remain committed, got %q", got)
		}
	})
}

func TestListCommandBranchAndAllScopes(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		currentTimestamp := "20200101T000000Z"
		featureTimestamp := "20210101T000000Z"
		currentRef := createAutosnapTestCommitRef(t, repo, branchRef, currentTimestamp, "current branch checkpoint")
		createAutosnapTestCommitRef(t, repo, "feature/foo", featureTimestamp, "feature branch checkpoint")
		currentDisplayTimestamp := formatCheckpointTimestampForList(currentTimestamp)
		featureDisplayTimestamp := formatCheckpointTimestampForList(featureTimestamp)

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
		if !strings.Contains(output, featureDisplayTimestamp) || !strings.Contains(output, "feature branch checkpoint") {
			t.Fatalf("expected feature checkpoint in branch list output, got %q", output)
		}
		if strings.Contains(output, featureTimestamp) {
			t.Fatalf("expected branch list output to use formatted timestamp, got %q", output)
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
		if !strings.Contains(output, currentDisplayTimestamp) || !strings.Contains(output, featureDisplayTimestamp) {
			t.Fatalf("expected all list output to include both timestamps, got %q", output)
		}
		if strings.Contains(output, currentTimestamp) || strings.Contains(output, featureTimestamp) {
			t.Fatalf("expected all list output to use formatted timestamps, got %q", output)
		}
	})
}

func TestListCommandListsInclusiveCheckpointRange(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ref1, _, _ := createCheckpointRangeScenario(t, repo)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "first+1..last"})

		if err := root.Execute(); err != nil {
			t.Fatalf("list checkpoint range failed: %v", err)
		}

		output := buf.String()
		if strings.Contains(output, "first") || strings.Contains(output, checkpointRefTimestamp(ref1)) {
			t.Fatalf("expected range list to exclude first checkpoint, got %q", output)
		}
		secondIndex := strings.Index(output, "second")
		thirdIndex := strings.Index(output, "third")
		if secondIndex < 0 || thirdIndex < 0 {
			t.Fatalf("expected range list to include second and third checkpoints, got %q", output)
		}
		if secondIndex > thirdIndex {
			t.Fatalf("expected range list to preserve chronological order, got %q", output)
		}
	})
}

func TestListCommandListsSingleCheckpointSelector(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		createCheckpointRangeScenario(t, repo)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "last-1"})

		if err := root.Execute(); err != nil {
			t.Fatalf("list single checkpoint selector failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "second") {
			t.Fatalf("expected single selector list to include second checkpoint, got %q", output)
		}
		if strings.Contains(output, "first") || strings.Contains(output, "third") {
			t.Fatalf("expected single selector list to include only second checkpoint, got %q", output)
		}
	})
}

func TestMarkCommandLabelsListShowAndJSONOutput(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "first checkpoint")
		createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000001Z", "second checkpoint")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newMarkCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"mark", "--bad", "--reason", "regression", "last"})
		if err := root.Execute(); err != nil {
			t.Fatalf("mark bad failed: %v", err)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list"})
		if err := root.Execute(); err != nil {
			t.Fatalf("list failed: %v", err)
		}
		output := buf.String()
		if !strings.Contains(output, "first checkpoint") || strings.Contains(output, "[unmarked]") || !strings.Contains(output, "[bad] second checkpoint") {
			t.Fatalf("expected list output to include compact good/bad labels, got %q", output)
		}
		if strings.Contains(output, "regression") {
			t.Fatalf("expected list text output to omit bad reason, got %q", output)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--format", "json"})
		if err := root.Execute(); err != nil {
			t.Fatalf("json list failed: %v", err)
		}
		var rows []map[string]any
		if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
			t.Fatalf("decode json list failed: %v output=%q", err, buf.String())
		}
		if len(rows) != 2 {
			t.Fatalf("expected 2 json rows, got %d", len(rows))
		}
		if rows[0]["mark"] != "unmarked" {
			t.Fatalf("expected first checkpoint mark=unmarked, got %+v", rows[0])
		}
		if rows[1]["mark"] != "bad" || rows[1]["badReason"] != "regression" {
			t.Fatalf("expected second checkpoint bad reason in json, got %+v", rows[1])
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"show", "last", "--name-only"})
		if err := root.Execute(); err != nil {
			t.Fatalf("show failed: %v", err)
		}
		output = buf.String()
		if !strings.Contains(output, "mark: bad") || !strings.Contains(output, "mark reason: regression") {
			t.Fatalf("expected show output to include bad mark and reason, got %q", output)
		}
	})
}

func TestCheckpointMarksLoadsMarkedAndUnmarkedCheckpoints(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "first checkpoint")
		badRef := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000001Z", "second checkpoint")
		goodRef := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000002Z", "third checkpoint")

		if err := markCheckpointBad(context.Background(), repo, checkpointRefInfo{Ref: badRef, FullCommit: runGitOutput(t, repo, "rev-parse", badRef)}, "regression", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatalf("mark bad failed: %v", err)
		}
		if err := markCheckpointGood(context.Background(), repo, checkpointRefInfo{Ref: goodRef, FullCommit: runGitOutput(t, repo, "rev-parse", goodRef)}, time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC)); err != nil {
			t.Fatalf("mark good failed: %v", err)
		}

		refs, err := listCheckpointRefsForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("listCheckpointRefsForBranch failed: %v", err)
		}
		checkpoints, err := listCheckpointsFromRefs(context.Background(), repo, refs)
		if err != nil {
			t.Fatalf("listCheckpointsFromRefs failed: %v", err)
		}
		marks, err := checkpointMarks(context.Background(), repo, checkpoints)
		if err != nil {
			t.Fatalf("checkpointMarks failed: %v", err)
		}
		if marks[refs[0].Ref].Mark != checkpointMarkStateUnmarked {
			t.Fatalf("expected first checkpoint to be unmarked, got %+v", marks[refs[0].Ref])
		}
		if marks[badRef].Mark != checkpointMarkStateBad || marks[badRef].Reason != "regression" {
			t.Fatalf("expected bad checkpoint mark with reason, got %+v", marks[badRef])
		}
		if marks[goodRef].Mark != checkpointMarkStateGood {
			t.Fatalf("expected good checkpoint mark, got %+v", marks[goodRef])
		}
	})
}

func TestCheckpointMarksAppliesCommitMarkToDuplicateCheckpointRefs(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		firstRef := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "first checkpoint")
		commit := runGitOutput(t, repo, "rev-parse", firstRef)
		secondRef := snapshotRef(branchRef, "20260101T000001Z")
		runGit(t, repo, "update-ref", secondRef, commit)
		addGitNote(t, repo, checkpointMarkNoteRef, commit, `{"mark":"bad","reason":"same commit"}`)

		refs, err := listCheckpointRefsForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("listCheckpointRefsForBranch failed: %v", err)
		}
		checkpoints, err := listCheckpointsFromRefs(context.Background(), repo, refs)
		if err != nil {
			t.Fatalf("listCheckpointsFromRefs failed: %v", err)
		}
		marks, err := checkpointMarks(context.Background(), repo, checkpoints)
		if err != nil {
			t.Fatalf("checkpointMarks failed: %v", err)
		}
		if marks[firstRef].Mark != checkpointMarkStateBad || marks[secondRef].Mark != checkpointMarkStateBad {
			t.Fatalf("expected both duplicate refs to use commit mark, got first=%+v second=%+v", marks[firstRef], marks[secondRef])
		}
		if marks[firstRef].Reason != "same commit" || marks[secondRef].Reason != "same commit" {
			t.Fatalf("expected both duplicate refs to keep mark reason, got first=%+v second=%+v", marks[firstRef], marks[secondRef])
		}
	})
}

func TestCheckpointMarksRejectsInvalidMarkPayload(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		ref := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "first checkpoint")
		addGitNote(t, repo, checkpointMarkNoteRef, ref, `{not json`)

		refs, err := listCheckpointRefsForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("listCheckpointRefsForBranch failed: %v", err)
		}
		checkpoints, err := listCheckpointsFromRefs(context.Background(), repo, refs)
		if err != nil {
			t.Fatalf("listCheckpointsFromRefs failed: %v", err)
		}
		_, err = checkpointMarks(context.Background(), repo, checkpoints)
		if err == nil || !strings.Contains(err.Error(), "invalid checkpoint mark") {
			t.Fatalf("expected invalid checkpoint mark error, got %v", err)
		}
	})
}

func TestMarkCommandMarksRangeAndClearsWithGood(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		createCheckpointRangeScenario(t, repo)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newMarkCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"mark", "--bad", "first+1..last"})
		if err := root.Execute(); err != nil {
			t.Fatalf("mark range bad failed: %v", err)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list"})
		if err := root.Execute(); err != nil {
			t.Fatalf("list failed: %v", err)
		}
		output := buf.String()
		if !strings.Contains(output, "first") || strings.Contains(output, "[unmarked]") || !strings.Contains(output, "[bad] second") || !strings.Contains(output, "[bad] third") {
			t.Fatalf("expected range mark labels in list output, got %q", output)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newMarkCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"mark", "--good", "last"})
		if err := root.Execute(); err != nil {
			t.Fatalf("mark good failed: %v", err)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list"})
		if err := root.Execute(); err != nil {
			t.Fatalf("list after good failed: %v", err)
		}
		output = buf.String()
		if !strings.Contains(output, "[bad] second") || !strings.Contains(output, "[good] third") {
			t.Fatalf("expected good mark to clear last checkpoint, got %q", output)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newMarkCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"mark", "--unmark", "last"})
		if err := root.Execute(); err != nil {
			t.Fatalf("unmark failed: %v", err)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list"})
		if err := root.Execute(); err != nil {
			t.Fatalf("list after unmark failed: %v", err)
		}
		output = buf.String()
		if !strings.Contains(output, "[bad] second") || !strings.Contains(output, "third") || strings.Contains(output, "[unmarked]") {
			t.Fatalf("expected unmark to clear last checkpoint, got %q", output)
		}
	})
}

func TestListCommandListsRangeForBranchScope(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "current branch checkpoint")
		createAutosnapTestCommitRef(t, repo, "feature/foo", "20260101T000000Z", "feature first")
		createAutosnapTestCommitRef(t, repo, "feature/foo", "20260101T000001Z", "feature second")
		createAutosnapTestCommitRef(t, repo, "feature/foo", "20260101T000002Z", "feature third")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--branch", "feature/foo", "first+1..last"})

		if err := root.Execute(); err != nil {
			t.Fatalf("list branch checkpoint range failed: %v", err)
		}

		output := buf.String()
		if strings.Contains(output, "current branch checkpoint") || strings.Contains(output, "feature first") {
			t.Fatalf("expected branch range list to exclude out-of-range checkpoints, got %q", output)
		}
		if !strings.Contains(output, "feature second") || !strings.Contains(output, "feature third") {
			t.Fatalf("expected branch range list to include selected feature checkpoints, got %q", output)
		}
	})
}

func TestListCommandSinceDuration(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		oldTimestamp := time.Now().UTC().Add(-10 * 24 * time.Hour).Format("20060102T150405Z")
		newTimestamp := time.Now().UTC().Add(-1 * time.Hour).Format("20060102T150405Z")
		createAutosnapTestCommitRef(t, repo, branchRef, oldTimestamp, "old list since duration")
		createAutosnapTestCommitRef(t, repo, branchRef, newTimestamp, "new list since duration")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--since", "7d"})
		if err := root.Execute(); err != nil {
			t.Fatalf("list since duration command failed: %v", err)
		}

		output := buf.String()
		if strings.Contains(output, "old list since duration") {
			t.Fatalf("expected old checkpoint to be excluded by since duration, got %q", output)
		}
		if !strings.Contains(output, "new list since duration") {
			t.Fatalf("expected new checkpoint in since duration output, got %q", output)
		}
	})
}

func TestListCommandSinceCheckpointCommitUsesCheckpointTimestamp(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		createAutosnapTestCommitRef(t, repo, branchRef, "20220101T000000Z", "old list since checkpoint")
		middleRef := createAutosnapTestCommitRef(t, repo, branchRef, "20220102T000000Z", "middle list since checkpoint")
		createAutosnapTestCommitRef(t, repo, branchRef, "20220103T000000Z", "new list since checkpoint")
		middleCommit := runGitOutput(t, repo, "rev-parse", middleRef)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--since", middleCommit})
		if err := root.Execute(); err != nil {
			t.Fatalf("list since checkpoint command failed: %v", err)
		}

		output := buf.String()
		if strings.Contains(output, "old list since checkpoint") {
			t.Fatalf("expected older checkpoint to be excluded by checkpoint cutoff, got %q", output)
		}
		if !strings.Contains(output, "middle list since checkpoint") || !strings.Contains(output, "new list since checkpoint") {
			t.Fatalf("expected cutoff checkpoint and newer checkpoint, got %q", output)
		}
	})
}

func TestListCommandRejectsAllScopeWithRange(t *testing.T) {
	t.Parallel()
	root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newListCommand())
	root.SetArgs([]string{"list", "--all", "first..last"})

	err := root.Execute()
	if err == nil {
		t.Fatalf("expected list --all with range to fail")
	}
	if !strings.Contains(err.Error(), "checkpoint ranges only for current branch or --branch") {
		t.Fatalf("expected all scope range error, got %v", err)
	}
}

func TestListCommandRejectsMalformedRange(t *testing.T) {
	root := NewRootCommand()
	root.SetArgs([]string{"list", "first..last..last"})

	err := root.Execute()
	if err == nil {
		t.Fatalf("expected malformed checkpoint range to fail")
	}
	if !strings.Contains(err.Error(), "invalid checkpoint range") {
		t.Fatalf("expected invalid checkpoint range error, got %v", err)
	}
}

func TestListCommandJSONLOutput(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		ref := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "jsonl checkpoint")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--format", "jsonl"})

		if err := root.Execute(); err != nil {
			t.Fatalf("list jsonl command failed: %v", err)
		}

		rows := parseJSONLRows(t, buf.String())
		if len(rows) != 1 {
			t.Fatalf("expected one JSONL row, got %d: %q", len(rows), buf.String())
		}
		row := rows[0]
		if row["ref"] != ref || row["branch"] != branchRef || row["summary"] != "jsonl checkpoint" {
			t.Fatalf("unexpected JSONL row: %#v", row)
		}
		if row["timestamp"] != formatCheckpointTimestampForList("20260101T000000Z") {
			t.Fatalf("expected formatted timestamp, got %#v", row["timestamp"])
		}
	})
}

func TestListCommandShowsWorktreeMatchMarkers(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "head checkpoint")
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("staged\n"), 0o644); err != nil {
			t.Fatalf("write staged file failed: %v", err)
		}
		runGit(t, repo, "add", "file.txt")
		indexTree := runGitOutput(t, repo, "write-tree")
		createAutosnapTestCommitRefFromTree(t, repo, branchRef, "20260101T000001Z", indexTree, "index checkpoint")

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("worktree\n"), 0o644); err != nil {
			t.Fatalf("write worktree file failed: %v", err)
		}
		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}
		worktreeTree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("compute worktree tree failed: %v", err)
		}
		createAutosnapTestCommitRefFromTree(t, repo, branchRef, "20260101T000002Z", worktreeTree, "worktree checkpoint")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list"})
		if err := root.Execute(); err != nil {
			t.Fatalf("list command failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "** worktree checkpoint") {
			t.Fatalf("expected worktree match marker in list output, got %q", output)
		}
		if !strings.Contains(output, "*  index checkpoint") {
			t.Fatalf("expected index match marker in list output, got %q", output)
		}
	})
}

func TestListCommandJSONLIncludesWorktreeMatch(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "head checkpoint")
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("staged\n"), 0o644); err != nil {
			t.Fatalf("write staged file failed: %v", err)
		}
		runGit(t, repo, "add", "file.txt")
		indexTree := runGitOutput(t, repo, "write-tree")
		indexRef := createAutosnapTestCommitRefFromTree(t, repo, branchRef, "20260101T000001Z", indexTree, "index checkpoint")

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("worktree\n"), 0o644); err != nil {
			t.Fatalf("write worktree file failed: %v", err)
		}
		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}
		worktreeTree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("compute worktree tree failed: %v", err)
		}
		worktreeRef := createAutosnapTestCommitRefFromTree(t, repo, branchRef, "20260101T000002Z", worktreeTree, "worktree checkpoint")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--format", "jsonl"})
		if err := root.Execute(); err != nil {
			t.Fatalf("list jsonl command failed: %v", err)
		}

		byRef := jsonRowsByRef(parseJSONLRows(t, buf.String()))
		if byRef[worktreeRef]["worktreeMatch"] != string(checkpointWorktreeMatchWorktree) {
			t.Fatalf("expected worktree JSON marker, got %#v", byRef[worktreeRef])
		}
		if byRef[indexRef]["worktreeMatch"] != string(checkpointWorktreeMatchIndex) {
			t.Fatalf("expected index JSON marker, got %#v", byRef[indexRef])
		}
	})
}

func TestListCommandJSONLOutputIncludesDecodedJSONNote(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		ref := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "noted checkpoint")
		addGitNote(t, repo, "refs/notes/diffcog", ref, `{"risk":"low","files":["a.go"]}`)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--format", "jsonl", "--notes-json", "--note-ref", "refs/notes/diffcog"})

		if err := root.Execute(); err != nil {
			t.Fatalf("list jsonl notes command failed: %v", err)
		}

		rows := parseJSONLRows(t, buf.String())
		if len(rows) != 1 {
			t.Fatalf("expected one JSONL row, got %d: %q", len(rows), buf.String())
		}
		note, ok := rows[0]["note"].(map[string]any)
		if !ok {
			t.Fatalf("expected decoded note object, got %#v", rows[0]["note"])
		}
		if note["risk"] != "low" {
			t.Fatalf("expected decoded note risk, got %#v", note)
		}
		files, ok := note["files"].([]any)
		if !ok || len(files) != 1 || files[0] != "a.go" {
			t.Fatalf("expected decoded note files, got %#v", note["files"])
		}
		if rows[0]["noteRef"] != "refs/notes/diffcog" {
			t.Fatalf("expected noteRef in row, got %#v", rows[0])
		}
	})
}

func TestListCommandJSONLNotesUseConfiguredNoteRef(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		if err := os.WriteFile(autosnapConfigPath(repo), []byte("note_ref = \"refs/notes/configured\"\n"), 0o644); err != nil {
			t.Fatalf("write config failed: %v", err)
		}
		ref := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "configured note checkpoint")
		addGitNote(t, repo, "refs/notes/configured", ref, `{"source":"config"}`)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--format", "jsonl", "--notes-json"})

		if err := root.Execute(); err != nil {
			t.Fatalf("list configured note ref failed: %v", err)
		}

		rows := parseJSONLRows(t, buf.String())
		if len(rows) != 1 {
			t.Fatalf("expected one JSONL row, got %d: %q", len(rows), buf.String())
		}
		if rows[0]["noteRef"] != "refs/notes/configured" {
			t.Fatalf("expected configured noteRef, got %#v", rows[0])
		}
	})
}

func TestListCommandJSONOutputIncludesRawNoteString(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		ref := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "raw note checkpoint")
		addGitNote(t, repo, "refs/notes/diffcog", ref, `{"risk":"low"}`)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--format", "json", "--notes", "--note-ref", "refs/notes/diffcog"})

		if err := root.Execute(); err != nil {
			t.Fatalf("list json raw notes command failed: %v", err)
		}

		rows := parseJSONRows(t, buf.String())
		if len(rows) != 1 {
			t.Fatalf("expected one JSON row, got %d: %q", len(rows), buf.String())
		}
		if rows[0]["note"] != `{"risk":"low"}` {
			t.Fatalf("expected raw note string, got %#v", rows[0]["note"])
		}
	})
}

func TestListCommandTextOutputIncludesRawNoteString(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		ref := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "text note checkpoint")
		addGitNote(t, repo, "refs/notes/diffcog", ref, `{"risk":"text"}`)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--notes", "--note-ref", "refs/notes/diffcog"})

		if err := root.Execute(); err != nil {
			t.Fatalf("list text notes command failed: %v", err)
		}

		output := buf.String()
		if strings.Contains(strings.TrimSpace(output), "{") && strings.HasPrefix(strings.TrimSpace(output), "{") {
			t.Fatalf("expected text output, got JSON-looking output %q", output)
		}
		if !strings.Contains(output, "note_ref: refs/notes/diffcog") || !strings.Contains(output, `note: {"risk":"text"}`) {
			t.Fatalf("expected raw text note output, got %q", output)
		}
	})
}

func TestListCommandJSONLNotesReportMissingAndInvalidNotes(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		missingRef := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "missing note checkpoint")
		invalidRef := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000001Z", "invalid note checkpoint")
		addGitNote(t, repo, "refs/notes/diffcog", invalidRef, `{not json`)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newListCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"list", "--format", "jsonl", "--notes-json", "--note-ref", "refs/notes/diffcog"})

		if err := root.Execute(); err != nil {
			t.Fatalf("list missing/invalid notes failed: %v", err)
		}

		rows := parseJSONLRows(t, buf.String())
		if len(rows) != 2 {
			t.Fatalf("expected two JSONL rows, got %d: %q", len(rows), buf.String())
		}
		byRef := jsonRowsByRef(rows)
		if byRef[missingRef]["note"] != nil {
			t.Fatalf("expected missing note to be null, got %#v", byRef[missingRef])
		}
		if _, ok := byRef[missingRef]["noteError"]; ok {
			t.Fatalf("expected missing note not to set noteError, got %#v", byRef[missingRef])
		}
		if byRef[invalidRef]["note"] != nil {
			t.Fatalf("expected invalid note to be null, got %#v", byRef[invalidRef])
		}
		if noteError, ok := byRef[invalidRef]["noteError"].(string); !ok || !strings.Contains(noteError, "invalid JSON note") {
			t.Fatalf("expected invalid noteError, got %#v", byRef[invalidRef])
		}
	})
}

func TestListCommandJSONLNotesValidateFlags(t *testing.T) {
	t.Parallel()
	root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newListCommand())
	root.SetArgs([]string{"list", "--format", "text", "--notes-json", "--note-ref", "refs/notes/diffcog"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--format json or jsonl") {
		t.Fatalf("expected notes-json format error, got %v", err)
	}

	root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newListCommand())
	root.SetArgs([]string{"list", "--notes"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--note-ref") {
		t.Fatalf("expected missing note ref error, got %v", err)
	}

	root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newListCommand())
	root.SetArgs([]string{"list", "--format", "json", "--notes", "--notes-json", "--note-ref", "refs/notes/diffcog"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("expected mutually exclusive notes error, got %v", err)
	}
}

func TestListCommandRejectsMultipleScopes(t *testing.T) {
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
}

func TestListCheckpointsFromRefsIncludesFailedMetadata(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		validRef := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "visible checkpoint")
		missingRef := snapshotRef(branchRef, "20990101T000000Z-missing")

		checkpoints, err := listCheckpointsFromRefs(context.Background(), repo, []checkpointRefInfo{
			{
				Ref:       validRef,
				Commit:    "deadbeef",
				Timestamp: "20260101T000000Z",
				Branch:    branchRef,
			},
			{
				Ref:       missingRef,
				Commit:    "cafebabe",
				Timestamp: "20260102T000000Z",
				Branch:    branchRef,
			},
		})
		if err != nil {
			t.Fatalf("listCheckpointsFromRefs failed: %v", err)
		}
		if len(checkpoints) != 2 {
			t.Fatalf("expected both checkpoints to be returned, got %d", len(checkpoints))
		}

		summaries := map[string]string{}
		for _, checkpoint := range checkpoints {
			summaries[checkpoint.Ref] = checkpoint.Summary
		}
		if summaries[validRef] == failedCommitMetadataSummary {
			t.Fatalf("expected valid ref %s not to fail", validRef)
		}
		if summaries[missingRef] != failedCommitMetadataSummary {
			t.Fatalf("expected missing ref %s to fail metadata read, got %q", missingRef, summaries[missingRef])
		}
	})
}

func TestListCheckpointsFromRefsUsesFirstContentLine(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		ref := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "feat(logging): implement structured logging\n \nbody line")
		checkpoints, err := listCheckpointsFromRefs(context.Background(), repo, []checkpointRefInfo{
			{
				Ref:       ref,
				Commit:    "deadbeef",
				Timestamp: "20260101T000000Z",
				Branch:    branchRef,
			},
		})
		if err != nil {
			t.Fatalf("listCheckpointsFromRefs failed: %v", err)
		}
		if len(checkpoints) != 1 {
			t.Fatalf("expected one checkpoint, got %d", len(checkpoints))
		}
		if got, want := checkpoints[0].Summary, "feat(logging): implement structured logging"; got != want {
			t.Fatalf("expected summary %q, got %q", want, got)
		}
	})
}

func TestCheckpointTreeLinesUsesCachedTrees(t *testing.T) {
	t.Parallel()
	treeLines, err := checkpointTreeLines(context.Background(), "", []checkpointInfo{
		{Ref: "refs/autosnapshots/main/20260101T000000Z", Tree: "1111111111111111111111111111111111111111"},
		{Ref: "refs/autosnapshots/main/20260101T000001Z", Tree: "2222222222222222222222222222222222222222"},
	})
	if err != nil {
		t.Fatalf("checkpointTreeLines failed: %v", err)
	}
	if len(treeLines) != 2 || treeLines[0] != "1111111111111111111111111111111111111111" || treeLines[1] != "2222222222222222222222222222222222222222" {
		t.Fatalf("expected cached tree lines in order, got %#v", treeLines)
	}
}

func TestCheckpointTreeLinesResolvesOnlyMissingTreesInOrder(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		firstRef := createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "first checkpoint")
		if err := os.WriteFile(filepath.Join(repo, "mixed-cache.txt"), []byte("changed\n"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
		runGit(t, repo, "add", "mixed-cache.txt")
		secondRef := createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20260101T000001Z", "second checkpoint")
		firstTree := runGitOutput(t, repo, "rev-parse", firstRef+"^{tree}")
		secondTree := runGitOutput(t, repo, "rev-parse", secondRef+"^{tree}")

		treeLines, err := checkpointTreeLines(context.Background(), repo, []checkpointInfo{
			{Ref: firstRef, Tree: firstTree},
			{Ref: secondRef},
		})
		if err != nil {
			t.Fatalf("checkpointTreeLines failed: %v", err)
		}
		if len(treeLines) != 2 || treeLines[0] != firstTree || treeLines[1] != secondTree {
			t.Fatalf("expected mixed cached/resolved tree lines in order, got %#v want [%s %s]", treeLines, firstTree, secondTree)
		}
	})
}

func TestPendingCommandCurrentBranch(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		if _, err := os.Create(filepath.Join(repo, "file2.txt")); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
		runGit(t, repo, "add", "file2.txt")
		runGit(t, repo, "commit", "-m", "regular commit")
		_ = createAutosnapTestCommitRef(t, repo, branchRef, "20200102T000000Z", "pending checkpoint")
		runGit(t, repo, "commit", "--allow-empty", "-m", "regular commit 2")

		initial := runGitOutput(t, repo, "rev-parse", "HEAD~2")
		runGit(t, repo, "reset", "--hard", initial)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending command failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "pending checkpoint") {
			t.Fatalf("expected pending checkpoint output, got %q", output)
		}
	})
}

func TestPendingCommandJSONLOutput(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		if _, err := os.Create(filepath.Join(repo, "jsonl-pending.txt")); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
		runGit(t, repo, "add", "jsonl-pending.txt")
		runGit(t, repo, "commit", "-m", "jsonl pending base")
		pendingRef := createAutosnapTestCommitRef(t, repo, branchRef, "20200102T000000Z", "jsonl pending checkpoint")
		initial := runGitOutput(t, repo, "rev-parse", "HEAD~1")
		runGit(t, repo, "reset", "--hard", initial)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending", "--format", "jsonl"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending jsonl command failed: %v", err)
		}

		rows := parseJSONLRows(t, buf.String())
		if len(rows) != 1 {
			t.Fatalf("expected one JSONL row, got %d: %q", len(rows), buf.String())
		}
		if rows[0]["ref"] != pendingRef || rows[0]["summary"] != "jsonl pending checkpoint" {
			t.Fatalf("unexpected pending JSONL row: %#v", rows[0])
		}
		if _, ok := rows[0]["pendingStatus"]; ok {
			t.Fatalf("expected normal pending JSONL not to include pendingStatus, got %#v", rows[0])
		}
	})
}

func TestPendingCommandShowsWorktreeMatchMarkers(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "head checkpoint")
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("staged\n"), 0o644); err != nil {
			t.Fatalf("write staged file failed: %v", err)
		}
		runGit(t, repo, "add", "file.txt")
		indexTree := runGitOutput(t, repo, "write-tree")
		createAutosnapTestCommitRefFromTree(t, repo, branchRef, "20260101T000001Z", indexTree, "index checkpoint")

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("worktree\n"), 0o644); err != nil {
			t.Fatalf("write worktree file failed: %v", err)
		}
		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}
		worktreeTree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("compute worktree tree failed: %v", err)
		}
		createAutosnapTestCommitRefFromTree(t, repo, branchRef, "20260101T000002Z", worktreeTree, "worktree checkpoint")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending command failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "** worktree checkpoint") {
			t.Fatalf("expected worktree match marker in pending output, got %q", output)
		}
		if !strings.Contains(output, "*  index checkpoint") {
			t.Fatalf("expected index match marker in pending output, got %q", output)
		}
	})
}

func TestPendingExplainCommandShowsWorktreeMatchMarkers(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "head checkpoint")
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("staged\n"), 0o644); err != nil {
			t.Fatalf("write staged file failed: %v", err)
		}
		runGit(t, repo, "add", "file.txt")
		indexTree := runGitOutput(t, repo, "write-tree")
		createAutosnapTestCommitRefFromTree(t, repo, branchRef, "20260101T000001Z", indexTree, "index checkpoint")

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("worktree\n"), 0o644); err != nil {
			t.Fatalf("write worktree file failed: %v", err)
		}
		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}
		worktreeTree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("compute worktree tree failed: %v", err)
		}
		createAutosnapTestCommitRefFromTree(t, repo, branchRef, "20260101T000002Z", worktreeTree, "worktree checkpoint")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending", "--explain"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending explain command failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "pending  ") || !strings.Contains(output, "** worktree checkpoint") {
			t.Fatalf("expected worktree match marker in pending explain output, got %q", output)
		}
		if !strings.Contains(output, "pending  ") || !strings.Contains(output, "*  index checkpoint") {
			t.Fatalf("expected index match marker in pending explain output, got %q", output)
		}
	})
}

func TestPendingCommandJSONLIncludesWorktreeMatch(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		createAutosnapTestCommitRef(t, repo, branchRef, "20260101T000000Z", "head checkpoint")
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("staged\n"), 0o644); err != nil {
			t.Fatalf("write staged file failed: %v", err)
		}
		runGit(t, repo, "add", "file.txt")
		indexTree := runGitOutput(t, repo, "write-tree")
		indexRef := createAutosnapTestCommitRefFromTree(t, repo, branchRef, "20260101T000001Z", indexTree, "index checkpoint")

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("worktree\n"), 0o644); err != nil {
			t.Fatalf("write worktree file failed: %v", err)
		}
		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}
		worktreeTree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("compute worktree tree failed: %v", err)
		}
		worktreeRef := createAutosnapTestCommitRefFromTree(t, repo, branchRef, "20260101T000002Z", worktreeTree, "worktree checkpoint")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending", "--format", "jsonl"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending jsonl command failed: %v", err)
		}

		byRef := jsonRowsByRef(parseJSONLRows(t, buf.String()))
		if byRef[worktreeRef]["worktreeMatch"] != string(checkpointWorktreeMatchWorktree) {
			t.Fatalf("expected worktree JSON marker, got %#v", byRef[worktreeRef])
		}
		if byRef[indexRef]["worktreeMatch"] != string(checkpointWorktreeMatchIndex) {
			t.Fatalf("expected index JSON marker, got %#v", byRef[indexRef])
		}
	})
}

func TestPendingExplainCommandJSONLOutputIncludesClassification(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "jsonl-explain.txt"), []byte("one\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint file failed: %v", err)
		}
		runGit(t, repo, "add", "jsonl-explain.txt")
		ref := createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20200104T000000Z", "jsonl explain checkpoint")
		addGitNote(t, repo, "refs/notes/diffcog", ref, `{"kind":"explain"}`)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending", "--explain", "--format", "jsonl", "--notes-json", "--note-ref", "refs/notes/diffcog"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending explain jsonl command failed: %v", err)
		}

		rows := parseJSONLRows(t, buf.String())
		if len(rows) != 1 {
			t.Fatalf("expected one JSONL row, got %d: %q", len(rows), buf.String())
		}
		if rows[0]["ref"] != ref || rows[0]["pendingStatus"] == "" {
			t.Fatalf("expected explain JSONL classification, got %#v", rows[0])
		}
		note, ok := rows[0]["note"].(map[string]any)
		if !ok || note["kind"] != "explain" {
			t.Fatalf("expected decoded explain note, got %#v", rows[0]["note"])
		}
	})
}

func TestPendingExplainJSONLClassificationUsesEarlyStop(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "older.txt"), []byte("older checkpoint\n"), 0o644); err != nil {
			t.Fatalf("write older checkpoint file failed: %v", err)
		}
		runGit(t, repo, "add", "older.txt")
		olderRef := createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20200103T000000Z", "older explain jsonl checkpoint")
		runGit(t, repo, "reset", "--hard", "HEAD")

		exactRef := createAutosnapTestCommitRef(t, repo, branchRef, "20200104T000000Z", "exact explain jsonl checkpoint")
		refs, err := listCheckpointRefsForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("listCheckpointRefsForBranch failed: %v", err)
		}

		debug := &bytes.Buffer{}
		statusByRef, err := classifyPendingCheckpointRefsStreamMap(context.Background(), repo, refs, branchRef, false, newPendingDebugLogger(debug, true))
		if err != nil {
			t.Fatalf("stream map classification failed: %v", err)
		}

		if statusByRef[exactRef] != checkpointStatusExact {
			t.Fatalf("expected exact checkpoint to be exact, got %q", statusByRef[exactRef])
		}
		if statusByRef[olderRef] != checkpointStatusObsolete {
			t.Fatalf("expected older checkpoint to be obsolete, got %q", statusByRef[olderRef])
		}
		if strings.Contains(debug.String(), "merge classification started") {
			t.Fatalf("expected exact newest checkpoint to avoid merge classification, got debug log: %s", debug.String())
		}
	})
}

func TestPendingCommandDebugWritesProgressToStderr(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		if _, err := os.Create(filepath.Join(repo, "debug.txt")); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
		runGit(t, repo, "add", "debug.txt")
		runGit(t, repo, "commit", "-m", "debug commit")
		_ = createAutosnapTestCommitRef(t, repo, branchRef, "20200103T000000Z", "debug pending checkpoint")
		initial := runGitOutput(t, repo, "rev-parse", "HEAD~1")
		runGit(t, repo, "reset", "--hard", initial)

		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(stdout)
		root.SetErr(stderr)
		root.SetArgs([]string{"pending", "--debug"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending debug command failed: %v", err)
		}

		out := stdout.String()
		if !strings.Contains(out, "debug pending checkpoint") {
			t.Fatalf("expected pending checkpoint output on stdout, got %q", out)
		}
		if strings.Contains(out, "debug: pending:") {
			t.Fatalf("expected debug output to stay off stdout, got %q", out)
		}

		errOut := stderr.String()
		for _, want := range []string{
			"debug: pending:",
			"listing checkpoint refs scope=current branch",
			"listed checkpoint refs count=",
			"classifying actionable checkpoints count=",
			"branch actionable classification started branch=" + branchRef,
			"merge classification started branch=" + branchRef,
			"loading checkpoint metadata count=",
		} {
			if !strings.Contains(errOut, want) {
				t.Fatalf("expected debug stderr to contain %q, got %q", want, errOut)
			}
		}
	})
}

func TestPendingCommandDebugExplainWritesMetadataProgressToStderr(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "debug-explain.txt"), []byte("one\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint file failed: %v", err)
		}
		runGit(t, repo, "add", "debug-explain.txt")
		_ = createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20200104T000000Z", "debug explain checkpoint")

		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(stdout)
		root.SetErr(stderr)
		root.SetArgs([]string{"pending", "--debug", "--explain"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending debug explain command failed: %v", err)
		}

		out := stdout.String()
		if !strings.Contains(out, "debug explain checkpoint") {
			t.Fatalf("expected explain checkpoint output on stdout, got %q", out)
		}
		if strings.Contains(out, "debug: pending:") {
			t.Fatalf("expected debug output to stay off stdout, got %q", out)
		}

		errOut := stderr.String()
		for _, want := range []string{
			"debug: pending:",
			"loading checkpoint metadata count=",
			"mode=explain",
			"loaded checkpoint metadata count=",
		} {
			if !strings.Contains(errOut, want) {
				t.Fatalf("expected debug stderr to contain %q, got %q", want, errOut)
			}
		}
	})
}

func TestPendingCommandLimitAppliesBeforeClassification(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "one.txt"), []byte("one\n"), 0o644); err != nil {
			t.Fatalf("write first checkpoint file failed: %v", err)
		}
		runGit(t, repo, "add", "one.txt")
		_ = createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20220101T000000Z", "old limited checkpoint")

		if err := os.WriteFile(filepath.Join(repo, "two.txt"), []byte("two\n"), 0o644); err != nil {
			t.Fatalf("write second checkpoint file failed: %v", err)
		}
		runGit(t, repo, "add", "two.txt")
		_ = createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20220102T000000Z", "new limited checkpoint")
		runGit(t, repo, "reset", "--hard", "HEAD")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending", "--limit", "1"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending limit command failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "new limited checkpoint") {
			t.Fatalf("expected newest checkpoint in limited output, got %q", output)
		}
		if strings.Contains(output, "old limited checkpoint") {
			t.Fatalf("expected older checkpoint to be excluded by limit, got %q", output)
		}
	})
}

func TestPendingExplainCommandLimitPreservesOutputOrder(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		_ = createAutosnapTestCommitRef(t, repo, branchRef, "20220101T000000Z", "old explain checkpoint")
		_ = createAutosnapTestCommitRef(t, repo, branchRef, "20220102T000000Z", "middle explain checkpoint")
		_ = createAutosnapTestCommitRef(t, repo, branchRef, "20220103T000000Z", "new explain checkpoint")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending", "--explain", "--limit", "2"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending explain limit command failed: %v", err)
		}

		output := buf.String()
		if strings.Contains(output, "old explain checkpoint") {
			t.Fatalf("expected oldest checkpoint to be excluded by limit, got %q", output)
		}
		middleIndex := strings.Index(output, "middle explain checkpoint")
		newIndex := strings.Index(output, "new explain checkpoint")
		if middleIndex < 0 || newIndex < 0 {
			t.Fatalf("expected middle and newest checkpoints in output, got %q", output)
		}
		if middleIndex > newIndex {
			t.Fatalf("expected limited explain output to remain oldest-to-newest, got %q", output)
		}
	})
}

func TestPendingCommandAllLimitIsGlobal(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		_ = createAutosnapTestCommitRef(t, repo, branchRef, "20220101T000000Z", "main old global limit")
		_ = createAutosnapTestCommitRef(t, repo, branchRef, "20220103T000000Z", "main new global limit")
		runGit(t, repo, "checkout", "-b", "feature/global-limit")
		_ = createAutosnapTestCommitRef(t, repo, "feature/global-limit", "20220104T000000Z", "feature new global limit")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending", "--all", "--explain", "--limit", "2"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending all explain limit command failed: %v", err)
		}

		output := buf.String()
		if strings.Contains(output, "main old global limit") {
			t.Fatalf("expected global limit to exclude oldest checkpoint, got %q", output)
		}
		if !strings.Contains(output, "main new global limit") || !strings.Contains(output, "feature new global limit") {
			t.Fatalf("expected global limit to include two newest checkpoints, got %q", output)
		}
	})
}

func TestPendingCommandSinceDurationFiltersByCheckpointTimestamp(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		oldTimestamp := time.Now().UTC().Add(-10 * 24 * time.Hour).Format("20060102T150405Z")
		newTimestamp := time.Now().UTC().Add(-1 * time.Hour).Format("20060102T150405Z")
		_ = createAutosnapTestCommitRef(t, repo, branchRef, oldTimestamp, "old since duration")
		_ = createAutosnapTestCommitRef(t, repo, branchRef, newTimestamp, "new since duration")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending", "--explain", "--since", "7d"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending since duration command failed: %v", err)
		}

		output := buf.String()
		if strings.Contains(output, "old since duration") {
			t.Fatalf("expected old checkpoint to be excluded by since duration, got %q", output)
		}
		if !strings.Contains(output, "new since duration") {
			t.Fatalf("expected new checkpoint in since duration output, got %q", output)
		}
	})
}

func TestPendingCommandSinceCheckpointCommitUsesCheckpointTimestamp(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		_ = createAutosnapTestCommitRef(t, repo, branchRef, "20220101T000000Z", "old since checkpoint")
		middleRef := createAutosnapTestCommitRef(t, repo, branchRef, "20220102T000000Z", "middle since checkpoint")
		_ = createAutosnapTestCommitRef(t, repo, branchRef, "20220103T000000Z", "new since checkpoint")
		middleCommit := runGitOutput(t, repo, "rev-parse", middleRef)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending", "--explain", "--since", middleCommit})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending since checkpoint command failed: %v", err)
		}

		output := buf.String()
		if strings.Contains(output, "old since checkpoint") {
			t.Fatalf("expected older checkpoint to be excluded by checkpoint cutoff, got %q", output)
		}
		if !strings.Contains(output, "middle since checkpoint") || !strings.Contains(output, "new since checkpoint") {
			t.Fatalf("expected cutoff checkpoint and newer checkpoint, got %q", output)
		}
	})
}

func TestPendingCommandSinceBranchCommitUsesAncestorOrSelf(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "old.txt"), []byte("old\n"), 0o644); err != nil {
			t.Fatalf("write old checkpoint file failed: %v", err)
		}
		runGit(t, repo, "add", "old.txt")
		_ = createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20220101T000000Z", "old since branch commit")
		runGit(t, repo, "reset", "--hard", "HEAD")

		if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o644); err != nil {
			t.Fatalf("write branch base file failed: %v", err)
		}
		runGit(t, repo, "add", "base.txt")
		runGit(t, repo, "commit", "-m", "branch since base")
		baseCommit := runGitOutput(t, repo, "rev-parse", "HEAD")

		if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new\n"), 0o644); err != nil {
			t.Fatalf("write new checkpoint file failed: %v", err)
		}
		runGit(t, repo, "add", "new.txt")
		_ = createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20220102T000000Z", "new since branch commit")
		runGit(t, repo, "reset", "--hard", "HEAD")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending", "--explain", "--since", baseCommit})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending since branch commit command failed: %v", err)
		}

		output := buf.String()
		if strings.Contains(output, "old since branch commit") {
			t.Fatalf("expected checkpoint before branch commit to be excluded, got %q", output)
		}
		if !strings.Contains(output, "new since branch commit") {
			t.Fatalf("expected descendant checkpoint in output, got %q", output)
		}
	})
}

func TestPendingCommandRejectsInvalidLimit(t *testing.T) {
	t.Parallel()
	root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newPendingCommand())
	root.SetArgs([]string{"pending", "--limit", "-1"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--limit") {
		t.Fatalf("expected invalid limit error, got %v", err)
	}
}

func TestPendingCommandRejectsPatchStatusWithoutExplain(t *testing.T) {
	t.Parallel()
	root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newPendingCommand())
	root.SetArgs([]string{"pending", "--patch-status"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--patch-status requires --explain") {
		t.Fatalf("expected patch status explain requirement error, got %v", err)
	}
}

func TestPendingCommandRejectsInvalidSince(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetArgs([]string{"pending", "--since", "not-a-commit"})
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--since") {
			t.Fatalf("expected invalid since error, got %v", err)
		}
	})
}

func TestPendingCommandSkipsOlderCheckpointsAfterNewestExact(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "conflict.txt"), []byte("checkpoint\n"), 0o644); err != nil {
			t.Fatalf("write older checkpoint file failed: %v", err)
		}
		runGit(t, repo, "add", "conflict.txt")
		olderRef := createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20200105T000000Z", "older conflict checkpoint")
		runGit(t, repo, "reset", "--hard", "HEAD")

		if err := os.WriteFile(filepath.Join(repo, "conflict.txt"), []byte("branch\n"), 0o644); err != nil {
			t.Fatalf("write branch conflict file failed: %v", err)
		}
		runGit(t, repo, "add", "conflict.txt")
		runGit(t, repo, "commit", "-m", "branch conflict commit")
		_ = createAutosnapTestCommitRef(t, repo, branchRef, "20200106T000000Z", "exact checkpoint")

		if err := os.WriteFile(filepath.Join(repo, "pending.txt"), []byte("pending\n"), 0o644); err != nil {
			t.Fatalf("write pending checkpoint file failed: %v", err)
		}
		runGit(t, repo, "add", "pending.txt")
		_ = createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20200107T000000Z", "newer pending checkpoint")
		runGit(t, repo, "reset", "--hard", "HEAD")

		stdout := &bytes.Buffer{}
		stderr := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(stdout)
		root.SetErr(stderr)
		root.SetArgs([]string{"pending", "--debug"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending debug command failed: %v", err)
		}

		out := stdout.String()
		if !strings.Contains(out, "newer pending checkpoint") {
			t.Fatalf("expected newer pending checkpoint output, got %q", out)
		}
		if strings.Contains(out, "older conflict checkpoint") || strings.Contains(out, "exact checkpoint") {
			t.Fatalf("expected older and exact checkpoints to be hidden, got %q", out)
		}

		errOut := stderr.String()
		if !strings.Contains(errOut, "order=newest-first") || !strings.Contains(errOut, "early stop branch="+branchRef) {
			t.Fatalf("expected newest-first early stop debug output, got %q", errOut)
		}
		if strings.Contains(errOut, "merge classification started branch="+branchRef+" index=1/3 ref="+olderRef) {
			t.Fatalf("expected older checkpoint merge classification to be skipped, got %q", errOut)
		}
	})
}

func TestPendingCommandBranchAndAllScopes(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		mainInitial := runGitOutput(t, repo, "rev-parse", "HEAD")

		if _, err := os.Create(filepath.Join(repo, "main.txt")); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
		runGit(t, repo, "add", "main.txt")
		runGit(t, repo, "commit", "-m", "main commit")
		_ = createAutosnapTestCommitRef(t, repo, branchRef, "20210101T000000Z", "main pending")
		runGit(t, repo, "reset", "--hard", mainInitial)

		runGit(t, repo, "checkout", "-b", "feature/foo")
		if _, err := os.Create(filepath.Join(repo, "feature.txt")); err != nil {
			t.Fatalf("write file failed: %v", err)
		}
		runGit(t, repo, "add", "feature.txt")
		runGit(t, repo, "commit", "-m", "feature commit")
		_ = createAutosnapTestCommitRef(t, repo, "feature/foo", "20210103T000000Z", "feature pending")
		runGit(t, repo, "commit", "--allow-empty", "-m", "feature same-tree commit")
		featureInitial := runGitOutput(t, repo, "rev-parse", "HEAD~2")
		runGit(t, repo, "reset", "--hard", featureInitial)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending", "--branch", "feature/foo"})

		if err := root.Execute(); err != nil {
			t.Fatalf("pending branch failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "feature pending") {
			t.Fatalf("expected feature pending output, got %q", output)
		}
		if strings.Contains(output, "main pending") {
			t.Fatalf("expected feature scope to exclude main pending checkpoint, got %q", output)
		}

		buf.Reset()
		allRoot := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		allRoot.AddCommand(newPendingCommand())
		allRoot.SetOut(buf)
		allRoot.SetErr(buf)
		allRoot.SetArgs([]string{"pending", "--all"})
		if err := allRoot.Execute(); err != nil {
			t.Fatalf("pending all failed: %v", err)
		}

		output = buf.String()
		if !strings.Contains(output, "main pending") || !strings.Contains(output, "feature pending") {
			t.Fatalf("expected all output to include both pending checkpoints, got %q", output)
		}
		if !strings.Contains(output, branchRef) || !strings.Contains(output, "feature/foo") || !strings.Contains(output, "main pending") || !strings.Contains(output, "feature pending") {
			t.Fatalf("expected all output to include both branch names and summaries, got %q", output)
		}
	})
}

func TestPendingCommandListsCheckpointsAfterLatestHeadMatch(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("one\n"), 0o644); err != nil {
			t.Fatalf("write first checkpoint file failed: %v", err)
		}
		runGit(t, repo, "add", "file.txt")
		_ = createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20210104T000000Z", "synced checkpoint")
		runGit(t, repo, "commit", "-m", "manual synced commit")

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("two\n"), 0o644); err != nil {
			t.Fatalf("write pending checkpoint file failed: %v", err)
		}
		runGit(t, repo, "add", "file.txt")
		_ = createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20210105T000000Z", "pending checkpoint")
		runGit(t, repo, "reset", "--hard", "HEAD")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending command failed: %v", err)
		}

		output := buf.String()
		if strings.Contains(output, "synced checkpoint") {
			t.Fatalf("expected synced checkpoint to be excluded, got %q", output)
		}
		if !strings.Contains(output, "pending checkpoint") {
			t.Fatalf("expected pending checkpoint output, got %q", output)
		}
	})
}

func TestPendingCommandHidesManuallyIntegratedCheckpoint(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "database.txt"), []byte("mysql\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint file failed: %v", err)
		}
		runGit(t, repo, "add", "database.txt")
		_ = createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20210108T000000Z", "integrated checkpoint")

		if err := os.WriteFile(filepath.Join(repo, "extra.txt"), []byte("manual cleanup\n"), 0o644); err != nil {
			t.Fatalf("write extra file failed: %v", err)
		}
		runGit(t, repo, "add", "extra.txt")
		runGit(t, repo, "commit", "-m", "manual integrated commit")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending command failed: %v", err)
		}
		if output := buf.String(); strings.Contains(output, "integrated checkpoint") || !strings.Contains(output, "no pending checkpoints") {
			t.Fatalf("expected integrated checkpoint to be hidden, got %q", output)
		}

		buf.Reset()
		explainRoot := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		explainRoot.AddCommand(newPendingCommand())
		explainRoot.SetOut(buf)
		explainRoot.SetErr(buf)
		explainRoot.SetArgs([]string{"pending", "--explain"})
		if err := explainRoot.Execute(); err != nil {
			t.Fatalf("pending explain command failed: %v", err)
		}
		if output := buf.String(); !strings.Contains(output, "integrated checkpoint") || !strings.Contains(output, " integrated ") {
			t.Fatalf("expected explain output to show integrated checkpoint, got %q", output)
		}
	})
}

func TestPendingExplainShowsPatchStatusColumn(t *testing.T) {
	t.Parallel()
	requireIntegration(t)

	tests := []struct {
		name            string
		worktreeContent string
		wantStatus      string
		wantReason      string
	}{
		{name: "included", worktreeContent: "checkpoint\n", wantStatus: string(checkpointPatchStatusIncluded), wantReason: "reason=tree_paths_included"},
		{name: "missing", worktreeContent: "base\n", wantStatus: string(checkpointPatchStatusMissing), wantReason: "reason=tree_paths_missing"},
		{name: "conflict", worktreeContent: "manual\n", wantStatus: string(checkpointPatchStatusConflict), wantReason: "reason=tree_paths_conflict"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := createTestRepo(t)
			withWorkingDir(t, repo, func() {
				_, _, branchRef, err := detectRepository(context.Background())
				if err != nil {
					t.Fatalf("detectRepository failed: %v", err)
				}

				if err := os.WriteFile(filepath.Join(repo, "patch-status.txt"), []byte("base\n"), 0o644); err != nil {
					t.Fatalf("write base file failed: %v", err)
				}
				runGit(t, repo, "add", "patch-status.txt")
				_ = createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20210108T000000Z", "patch base checkpoint")

				if err := os.WriteFile(filepath.Join(repo, "patch-status.txt"), []byte("checkpoint\n"), 0o644); err != nil {
					t.Fatalf("write checkpoint file failed: %v", err)
				}
				runGit(t, repo, "add", "patch-status.txt")
				_ = createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20210108T000001Z", "patch target checkpoint")

				if err := os.WriteFile(filepath.Join(repo, "patch-status.txt"), []byte(tt.worktreeContent), 0o644); err != nil {
					t.Fatalf("write worktree file failed: %v", err)
				}

				buf := &bytes.Buffer{}
				root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
				root.AddCommand(newPendingCommand())
				root.SetOut(buf)
				root.SetErr(buf)
				root.SetArgs([]string{"pending", "--debug", "--explain", "--patch-status"})
				if err := root.Execute(); err != nil {
					t.Fatalf("pending explain failed: %v", err)
				}

				line := lineContaining(buf.String(), "patch target checkpoint")
				if line == "" {
					t.Fatalf("expected target checkpoint line, got %q", buf.String())
				}
				if !strings.Contains(line, " "+tt.wantStatus+" ") {
					t.Fatalf("expected patch status %q in target line %q", tt.wantStatus, line)
				}
				if !strings.Contains(buf.String(), tt.wantReason) {
					t.Fatalf("expected debug output to contain %q, got %q", tt.wantReason, buf.String())
				}
				if strings.Contains(buf.String(), "loading patch statuses count=") {
					t.Fatalf("expected text explain output to avoid upfront patch status loading, got %q", buf.String())
				}
				rowIndex := strings.Index(buf.String(), "patch target checkpoint")
				summaryIndex := strings.Index(buf.String(), "patch status classification finished")
				if rowIndex < 0 || summaryIndex < 0 || rowIndex > summaryIndex {
					t.Fatalf("expected text row before final patch status summary, got %q", buf.String())
				}

				buf.Reset()
				root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
				root.AddCommand(newPendingCommand())
				root.SetOut(buf)
				root.SetErr(buf)
				root.SetArgs([]string{"pending", "--explain"})
				if err := root.Execute(); err != nil {
					t.Fatalf("pending explain without patch status failed: %v", err)
				}
				line = lineContaining(buf.String(), "patch target checkpoint")
				if line == "" {
					t.Fatalf("expected target checkpoint line, got %q", buf.String())
				}
				if strings.Contains(line, " "+tt.wantStatus+" ") {
					t.Fatalf("expected default explain output to omit patch status %q in target line %q", tt.wantStatus, line)
				}
			})
		})
	}
}

func TestPendingExplainPatchStatusFastPathAddedAndDeletedFiles(t *testing.T) {
	t.Parallel()
	requireIntegration(t)

	tests := []struct {
		name       string
		baseFiles  map[string]string
		patchFiles map[string]*string
		target     map[string]string
		wantStatus string
	}{
		{
			name:       "added file included",
			baseFiles:  map[string]string{"unchanged.txt": "base\n"},
			patchFiles: map[string]*string{"added.txt": stringPointer("checkpoint\n")},
			target:     map[string]string{"unchanged.txt": "base\n", "added.txt": "checkpoint\n"},
			wantStatus: string(checkpointPatchStatusIncluded),
		},
		{
			name:       "added file missing",
			baseFiles:  map[string]string{"unchanged.txt": "base\n"},
			patchFiles: map[string]*string{"added.txt": stringPointer("checkpoint\n")},
			target:     map[string]string{"unchanged.txt": "base\n"},
			wantStatus: string(checkpointPatchStatusMissing),
		},
		{
			name:       "deleted file included",
			baseFiles:  map[string]string{"deleted.txt": "base\n", "unchanged.txt": "base\n"},
			patchFiles: map[string]*string{"deleted.txt": nil},
			target:     map[string]string{"unchanged.txt": "base\n"},
			wantStatus: string(checkpointPatchStatusIncluded),
		},
		{
			name:       "deleted file missing",
			baseFiles:  map[string]string{"deleted.txt": "base\n", "unchanged.txt": "base\n"},
			patchFiles: map[string]*string{"deleted.txt": nil},
			target:     map[string]string{"deleted.txt": "base\n", "unchanged.txt": "base\n"},
			wantStatus: string(checkpointPatchStatusMissing),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := createTestRepo(t)
			withWorkingDir(t, repo, func() {
				_, _, branchRef, err := detectRepository(context.Background())
				if err != nil {
					t.Fatalf("detectRepository failed: %v", err)
				}

				writeTestFiles(t, repo, tt.baseFiles)
				runGit(t, repo, "add", ".")
				_ = createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20210108T000000Z", "base checkpoint")

				for path, content := range tt.patchFiles {
					fullPath := filepath.Join(repo, path)
					if content == nil {
						if err := os.Remove(fullPath); err != nil {
							t.Fatalf("remove checkpoint file failed: %v", err)
						}
						continue
					}
					if err := os.WriteFile(fullPath, []byte(*content), 0o644); err != nil {
						t.Fatalf("write checkpoint file failed: %v", err)
					}
				}
				runGit(t, repo, "add", ".")
				_ = createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20210108T000001Z", "target checkpoint")

				runGit(t, repo, "reset", "--hard", "HEAD")
				runGit(t, repo, "clean", "-fd")
				writeTestFiles(t, repo, tt.target)

				buf := &bytes.Buffer{}
				root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
				root.AddCommand(newPendingCommand())
				root.SetOut(buf)
				root.SetErr(buf)
				root.SetArgs([]string{"pending", "--debug", "--explain", "--patch-status"})
				if err := root.Execute(); err != nil {
					t.Fatalf("pending explain failed: %v", err)
				}

				line := lineContaining(buf.String(), "target checkpoint")
				if line == "" {
					t.Fatalf("expected target checkpoint line, got %q", buf.String())
				}
				if !strings.Contains(line, " "+tt.wantStatus+" ") {
					t.Fatalf("expected patch status %q in target line %q", tt.wantStatus, line)
				}
				if !strings.Contains(buf.String(), "reason=tree_paths_") {
					t.Fatalf("expected tree-path fast path debug reason, got %q", buf.String())
				}
			})
		})
	}
}

func TestPendingExplainJSONLIncludesPatchStatus(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "patch-status-json.txt"), []byte("base\n"), 0o644); err != nil {
			t.Fatalf("write base file failed: %v", err)
		}
		runGit(t, repo, "add", "patch-status-json.txt")
		_ = createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20210108T000000Z", "json patch base checkpoint")

		if err := os.WriteFile(filepath.Join(repo, "patch-status-json.txt"), []byte("checkpoint\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint file failed: %v", err)
		}
		runGit(t, repo, "add", "patch-status-json.txt")
		targetRef := createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20210108T000001Z", "json patch target checkpoint")

		if err := os.WriteFile(filepath.Join(repo, "patch-status-json.txt"), []byte("checkpoint\n"), 0o644); err != nil {
			t.Fatalf("write worktree file failed: %v", err)
		}

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending", "--explain", "--format", "jsonl"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending explain jsonl failed: %v", err)
		}

		rows := jsonRowsByRef(parseJSONLRows(t, buf.String()))
		row := rows[targetRef]
		if row == nil {
			t.Fatalf("expected target row in JSONL output, got %q", buf.String())
		}
		if _, ok := row["patchStatus"]; ok {
			t.Fatalf("expected default JSONL output to omit patchStatus, got %#v", row)
		}

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending", "--explain", "--patch-status", "--format", "jsonl"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending explain jsonl with patch status failed: %v", err)
		}

		rows = jsonRowsByRef(parseJSONLRows(t, buf.String()))
		row = rows[targetRef]
		if row == nil {
			t.Fatalf("expected target row in JSONL output, got %q", buf.String())
		}
		if row["patchStatus"] != string(checkpointPatchStatusIncluded) {
			t.Fatalf("expected patchStatus included, got %#v", row)
		}
	})
}

func TestParseCheckpointPatchChangedPaths(t *testing.T) {
	t.Parallel()
	output := "M\x00file.txt\x00A\x00added.txt\x00D\x00deleted.txt\x00M\x00file.txt\x00"
	got := parseCheckpointPatchChangedPaths(output)
	want := []string{"file.txt", "added.txt", "deleted.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected changed paths %#v, got %#v", want, got)
	}
}

func TestPendingCommandMarksOlderVariantsObsolete(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "docker-compose.yml"), []byte("mysql: 5.7\n"), 0o644); err != nil {
			t.Fatalf("write first variant failed: %v", err)
		}
		runGit(t, repo, "add", "docker-compose.yml")
		_ = createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20210109T000000Z", "obsolete checkpoint")

		if err := os.WriteFile(filepath.Join(repo, "docker-compose.yml"), []byte("mysql: 8.4\n"), 0o644); err != nil {
			t.Fatalf("write final variant failed: %v", err)
		}
		runGit(t, repo, "add", "docker-compose.yml")
		_ = createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20210110T000000Z", "exact checkpoint")
		runGit(t, repo, "commit", "-m", "manual final variant")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending command failed: %v", err)
		}
		if output := buf.String(); strings.Contains(output, "obsolete checkpoint") || strings.Contains(output, "exact checkpoint") || !strings.Contains(output, "no pending checkpoints") {
			t.Fatalf("expected obsolete and exact checkpoints to be hidden, got %q", output)
		}

		buf.Reset()
		explainRoot := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		explainRoot.AddCommand(newPendingCommand())
		explainRoot.SetOut(buf)
		explainRoot.SetErr(buf)
		explainRoot.SetArgs([]string{"pending", "--explain"})
		if err := explainRoot.Execute(); err != nil {
			t.Fatalf("pending explain command failed: %v", err)
		}
		output := buf.String()
		if !strings.Contains(output, "obsolete checkpoint") || !strings.Contains(output, " obsolete ") {
			t.Fatalf("expected explain output to mark older variant obsolete, got %q", output)
		}
		if !strings.Contains(output, "exact checkpoint") || !strings.Contains(output, " exact ") {
			t.Fatalf("expected explain output to mark final variant exact, got %q", output)
		}
	})
}

func TestPendingCommandShowsConflictAfterIntegratedCheckpoint(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "conflict.txt"), []byte("base\n"), 0o644); err != nil {
			t.Fatalf("write base file failed: %v", err)
		}
		runGit(t, repo, "add", "conflict.txt")
		runGit(t, repo, "commit", "-m", "base conflict file")
		createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20210111T000000Z", "integrated base checkpoint")

		if err := os.WriteFile(filepath.Join(repo, "conflict.txt"), []byte("checkpoint\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint conflict file failed: %v", err)
		}
		runGit(t, repo, "add", "conflict.txt")
		_ = createAutosnapTestCommitRefFromIndex(t, repo, branchRef, "20210112T000000Z", "conflict checkpoint")

		runGit(t, repo, "reset", "--hard", "HEAD")
		if err := os.WriteFile(filepath.Join(repo, "conflict.txt"), []byte("branch\n"), 0o644); err != nil {
			t.Fatalf("write branch conflict file failed: %v", err)
		}
		runGit(t, repo, "add", "conflict.txt")
		runGit(t, repo, "commit", "-m", "branch conflict change")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending command failed: %v", err)
		}
		if output := buf.String(); !strings.Contains(output, "conflict checkpoint") {
			t.Fatalf("expected conflict checkpoint to remain pending, got %q", output)
		}

		buf.Reset()
		explainRoot := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		explainRoot.AddCommand(newPendingCommand())
		explainRoot.SetOut(buf)
		explainRoot.SetErr(buf)
		explainRoot.SetArgs([]string{"pending", "--explain"})
		if err := explainRoot.Execute(); err != nil {
			t.Fatalf("pending explain command failed: %v", err)
		}
		if output := buf.String(); !strings.Contains(output, "conflict checkpoint") || !strings.Contains(output, " conflict ") {
			t.Fatalf("expected explain output to mark conflict checkpoint, got %q", output)
		}
	})
}

func TestPendingCommandAllSkipsDeletedBranches(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		if _, err := os.Create(filepath.Join(repo, "main.txt")); err != nil {
			t.Fatalf("write main file failed: %v", err)
		}
		runGit(t, repo, "add", "main.txt")
		runGit(t, repo, "commit", "-m", "main commit")
		_ = createAutosnapTestCommitRef(t, repo, branchRef, "20210106T000000Z", "main checkpoint")
		runGit(t, repo, "reset", "--hard", "HEAD~1")

		runGit(t, repo, "checkout", "-b", "deleted-branch")
		deletedRef := createAutosnapTestCommitRef(t, repo, "deleted-branch", "20210107T000000Z", "deleted branch checkpoint")
		runGit(t, repo, "checkout", branchRef)
		runGit(t, repo, "branch", "-D", "deleted-branch")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPendingCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pending", "--all"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pending all failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "main checkpoint") {
			t.Fatalf("expected all output to include resolvable branch checkpoint, got %q", output)
		}
		if strings.Contains(output, deletedRef) || strings.Contains(output, "deleted branch checkpoint") {
			t.Fatalf("expected all output to skip deleted branch checkpoint, got %q", output)
		}
	})
}

func TestPendingCommandRejectsMultipleScopes(t *testing.T) {
	t.Parallel()
	root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newPendingCommand())
	root.SetArgs([]string{"pending", "--branch", "feature/foo", "--all"})

	err := root.Execute()
	if err == nil {
		t.Fatalf("expected pending to fail")
	}
	if !strings.Contains(err.Error(), "at most one scope") {
		t.Fatalf("expected scope error, got %v", err)
	}
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

func TestRunCheckAttachesGitNoteWithMessageSourceEnv(t *testing.T) {
	t.Parallel()
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
		noteCommand := `printf 'base:%s
prev:%s
branch:%s
head:%s
commit:%s
' "$AUTOSNAP_DIFF_BASE" "$AUTOSNAP_PREVIOUS_CHECKPOINT_REF" "$AUTOSNAP_BRANCH_REF" "$AUTOSNAP_HEAD" "$AUTOSNAP_CHECKPOINT_COMMIT"`
		runner, err := newSnapshotRunner(ctx, repoRoot, branchRef, "true", "", snapshotModeBoth, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunner failed: %v", err)
		}
		runner.noteCommand = noteCommand
		runner.noteRef = "refs/notes/diffcog"

		if err := os.WriteFile(filepath.Join(repoRoot, "first.txt"), []byte("first"), 0o644); err != nil {
			t.Fatalf("write first file failed: %v", err)
		}
		runner.runCheck()

		if runner.state.LastCheckpointRef == "" {
			t.Fatalf("expected checkpoint ref in state")
		}
		ref := runner.state.LastCheckpointRef
		commit := runGitOutput(t, repoRoot, "rev-parse", ref)
		note := runGitOutput(t, repoRoot, "notes", "--ref", "refs/notes/diffcog", "show", commit)
		for _, want := range []string{
			"base:" + head,
			"prev:",
			"branch:" + branchRef,
			"head:" + head,
			"commit:" + commit,
		} {
			if !strings.Contains(note, want) {
				t.Fatalf("expected note to contain %q, got %q", want, note)
			}
		}
	})
}

func TestCheckpointCommandNoteFailureKeepsCheckpoint(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("note failure"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newCheckpointCommand())
		root.SetArgs([]string{"checkpoint", "--check", "true", "--note-command", "false", "--note-ref", "refs/notes/diffcog"})
		err = root.Execute()
		if err == nil || !strings.Contains(err.Error(), "checkpoint created but note attachment failed") {
			t.Fatalf("expected note attachment error after checkpoint creation, got %v", err)
		}

		checkpoints, err := listCheckpointRefsForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("list checkpoints failed: %v", err)
		}
		if len(checkpoints) != 1 {
			t.Fatalf("expected one checkpoint despite note failure, got %d", len(checkpoints))
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
		originalNow := currentTimestampFn
		timestamps := []string{"20260101T120000Z", "20260101T120001Z"}
		i := 0
		currentTimestampFn = func() string {
			value := timestamps[i]
			i++
			return value
		}
		t.Cleanup(func() { currentTimestampFn = originalNow })
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
		originalNow := currentTimestampFn
		timestamps := []string{"20260101T120000Z", "20260101T120001Z"}
		i := 0
		currentTimestampFn = func() string {
			value := timestamps[i]
			i++
			return value
		}
		t.Cleanup(func() { currentTimestampFn = originalNow })
		runner, err := newSnapshotRunner(ctx, repoRoot, branchRef, "true", msgSourceCmd, snapshotModeBoth, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunner failed: %v", err)
		}

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

func TestShowCommandRejectsTimestamp(t *testing.T) {
	t.Parallel()
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

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("timestamp checkpoint\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint failed: %v", err)
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

		err = root.Execute()
		if err == nil {
			t.Fatalf("expected show to reject timestamp identifier %q", timestamp)
		}
		if !strings.Contains(err.Error(), "checkpoint identifier") && !strings.Contains(err.Error(), "checkpoint not found") {
			t.Fatalf("expected rejection for unsupported show argument, got: %v", err)
		}
		if strings.Contains(err.Error(), ref) {
			t.Fatalf("expected no successful show output, got: %q", err.Error())
		}
	})
}

func TestShowCommandHelpDocumentsHistorySelectors(t *testing.T) {
	buf := &bytes.Buffer{}
	root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newShowCommand())
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"show", "--help"})

	if err := root.Execute(); err != nil {
		t.Fatalf("show --help failed: %v", err)
	}

	output := buf.String()
	for _, want := range []string{"show [checkpoint-or-range]", "defaults to the last checkpoint", "first+N", "last-N", "autosnap show\n", "autosnap show last-1", "--git-diff"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected show help to contain %q, got:\n%s", want, output)
		}
	}
}

func TestShowCommandSupportsFirstAndLastSelectors(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		originalNow := currentTimestampFn
		timestamps := []string{"20260101T120000Z", "20260101T120001Z", "20260101T120002Z"}
		i := 0
		currentTimestampFn = func() string {
			value := timestamps[i]
			i++
			return value
		}
		defer func() { currentTimestampFn = originalNow }()

		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("changed 1\n"), 0o644); err != nil {
			t.Fatalf("write first checkpoint failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		ref1, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "first")
		if err != nil {
			t.Fatalf("create first checkpoint failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("changed 2\n"), 0o644); err != nil {
			t.Fatalf("write second checkpoint failed: %v", err)
		}
		tree2, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree second failed: %v", err)
		}
		ref2, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree2, "second")
		if err != nil {
			t.Fatalf("create second checkpoint failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("changed 3\n"), 0o644); err != nil {
			t.Fatalf("write third checkpoint failed: %v", err)
		}
		tree3, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree third failed: %v", err)
		}
		ref3, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree3, "third")
		if err != nil {
			t.Fatalf("create third checkpoint failed: %v", err)
		}

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(buf)
		root.SetErr(buf)

		root.SetArgs([]string{"show", "first"})
		if err := root.Execute(); err != nil {
			t.Fatalf("show first failed: %v", err)
		}
		if !strings.Contains(buf.String(), "checkpoint: "+ref1) {
			t.Fatalf("expected first selector to resolve to first checkpoint, got %q", buf.String())
		}

		buf.Reset()
		root.SetArgs([]string{"show", "last"})
		if err := root.Execute(); err != nil {
			t.Fatalf("show last failed: %v", err)
		}
		if !strings.Contains(buf.String(), "checkpoint: "+ref3) {
			t.Fatalf("expected last selector to resolve to latest checkpoint, got %q", buf.String())
		}

		buf.Reset()
		root.SetArgs([]string{"show"})
		if err := root.Execute(); err != nil {
			t.Fatalf("show without checkpoint argument failed: %v", err)
		}
		if !strings.Contains(buf.String(), "checkpoint: "+ref3) {
			t.Fatalf("expected omitted selector to resolve to latest checkpoint, got %q", buf.String())
		}

		if ref1 == ref2 || ref2 == ref3 || ref1 == ref3 {
			t.Fatalf("expected generated refs to be distinct, got %q %q", ref2, ref3)
		}
	})
}

func TestShowCommandSupportsRelativeSelectors(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		originalNow := currentTimestampFn
		timestamps := []string{"20260101T120000Z", "20260101T120001Z", "20260101T120002Z"}
		i := 0
		currentTimestampFn = func() string {
			value := timestamps[i]
			i++
			return value
		}
		defer func() { currentTimestampFn = originalNow }()

		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("changed 1\n"), 0o644); err != nil {
			t.Fatalf("write first checkpoint failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		ref1, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "first")
		if err != nil {
			t.Fatalf("create first checkpoint failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("changed 2\n"), 0o644); err != nil {
			t.Fatalf("write second checkpoint failed: %v", err)
		}
		tree2, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree second failed: %v", err)
		}
		ref2, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree2, "second")
		if err != nil {
			t.Fatalf("create second checkpoint failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("changed 3\n"), 0o644); err != nil {
			t.Fatalf("write third checkpoint failed: %v", err)
		}
		tree3, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree third failed: %v", err)
		}
		ref3, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree3, "third")
		if err != nil {
			t.Fatalf("create third checkpoint failed: %v", err)
		}

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(buf)
		root.SetErr(buf)

		root.SetArgs([]string{"show", "first+1"})
		if err := root.Execute(); err != nil {
			t.Fatalf("show first+1 failed: %v", err)
		}
		if !strings.Contains(buf.String(), "checkpoint: "+ref2) {
			t.Fatalf("expected first+1 to resolve to second checkpoint, got %q", buf.String())
		}

		buf.Reset()
		root.SetArgs([]string{"show", "last-1"})
		if err := root.Execute(); err != nil {
			t.Fatalf("show last-1 failed: %v", err)
		}
		if !strings.Contains(buf.String(), "checkpoint: "+ref2) {
			t.Fatalf("expected last-1 to resolve to second checkpoint, got %q", buf.String())
		}

		buf.Reset()
		root.SetArgs([]string{"show", "first+2"})
		if err := root.Execute(); err != nil {
			t.Fatalf("show first+2 failed: %v", err)
		}
		if !strings.Contains(buf.String(), "checkpoint: "+ref3) {
			t.Fatalf("expected first+2 to resolve to third checkpoint, got %q", buf.String())
		}

		if ref3 == ref1 {
			t.Fatalf("expected distinct refs for first and third checkpoints, got %q", ref3)
		}
	})
}

func TestShowCommandErrorsOnOutOfRangeSelector(t *testing.T) {
	t.Parallel()
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

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("first\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		if _, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "first"); err != nil {
			t.Fatalf("create checkpoint failed: %v", err)
		}

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"show", "last-3"})

		execErr := root.Execute()
		if execErr == nil {
			t.Fatalf("expected show last-3 to fail")
		}
		if !strings.Contains(execErr.Error(), "out of range") {
			t.Fatalf("expected out-of-range error, got: %v", execErr)
		}
	})
}

func TestRestoreCommandRejectsTimestamp(t *testing.T) {
	t.Parallel()
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

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("restore timestamp checkpoint\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint failed: %v", err)
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
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newRestoreCommand())
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"restore", timestamp})

		err = root.Execute()
		if err == nil {
			t.Fatalf("expected restore to reject timestamp identifier %q", timestamp)
		}
		if !strings.Contains(err.Error(), "checkpoint identifier") && !strings.Contains(err.Error(), "checkpoint not found") {
			t.Fatalf("expected rejection for unsupported restore argument, got: %v", err)
		}
		if strings.Contains(err.Error(), ref) {
			t.Fatalf("expected no successful restore output, got: %q", err.Error())
		}
	})
}

func TestShowCommandResolvesCheckpointByRefAndCommit(t *testing.T) {
	t.Parallel()
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

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("show ref checkpoint\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint failed: %v", err)
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
	t.Parallel()
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

func TestShowCommandShowsPatchByDefault(t *testing.T) {
	t.Parallel()
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

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("checkpoint base\n"), 0o644); err != nil {
			t.Fatalf("write first checkpoint failed: %v", err)
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
		root.SetArgs([]string{"show", ref2})

		if err := root.Execute(); err != nil {
			t.Fatalf("show failed: %v", err)
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

		buf.Reset()
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"show", "--full", ref2})

		if err := root.Execute(); err != nil {
			t.Fatalf("show --full failed: %v", err)
		}
		if !strings.Contains(buf.String(), "diff --git") {
			t.Fatalf("expected --full compatibility flag to show patch output, got: %q", buf.String())
		}
	})
}

func TestShowCommandNameOnlyShowsChangedFileNames(t *testing.T) {
	t.Parallel()
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

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("checkpoint base\n"), 0o644); err != nil {
			t.Fatalf("write first checkpoint failed: %v", err)
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
		if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new\n"), 0o644); err != nil {
			t.Fatalf("write new file failed: %v", err)
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
		root.SetArgs([]string{"show", "--name-only", ref2})

		if err := root.Execute(); err != nil {
			t.Fatalf("show --name-only failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "file.txt") {
			t.Fatalf("expected changed file name in output, got: %q", output)
		}
		if !strings.Contains(output, "new.txt") {
			t.Fatalf("expected new file name in output, got: %q", output)
		}
		if strings.Contains(output, "diff --git") {
			t.Fatalf("expected name-only output without patch body, got: %q", output)
		}
	})
}

func TestShowCommandGitDiffShowsExactGitDiffOutput(t *testing.T) {
	t.Parallel()
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

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("checkpoint base\n"), 0o644); err != nil {
			t.Fatalf("write first checkpoint failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		ref1, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "")
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
		ref2, commit2, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree2, "")
		if err != nil {
			t.Fatalf("create checkpoint changed failed: %v", err)
		}

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"show", "--git-diff", ref2})

		if err := root.Execute(); err != nil {
			t.Fatalf("show --git-diff failed: %v", err)
		}

		output := buf.String()
		want := runGitOutput(t, repo, "diff", "--no-color", ref1, commit2)
		if strings.TrimSpace(output) != want {
			t.Fatalf("expected exact git diff output\nwant:\n%s\n\ngot:\n%s", want, output)
		}
		for _, forbidden := range []string{"checkpoint:", "commit:", "timestamp:", "status:", "check:"} {
			if strings.Contains(output, forbidden) {
				t.Fatalf("expected --git-diff output without %q metadata, got: %q", forbidden, output)
			}
		}
		if !strings.Contains(output, "diff --git") {
			t.Fatalf("expected git diff patch output, got: %q", output)
		}
		if !strings.Contains(output, "+++ b/file.txt") {
			t.Fatalf("expected file diff in output, got: %q", output)
		}
	})
}

func TestShowCommandGitDiffNameOnlyShowsExactGitDiffNameOnlyOutput(t *testing.T) {
	t.Parallel()
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

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("checkpoint base\n"), 0o644); err != nil {
			t.Fatalf("write first checkpoint failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		ref1, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "")
		if err != nil {
			t.Fatalf("create checkpoint failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("updated\n"), 0o644); err != nil {
			t.Fatalf("update file failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repo, "new.txt"), []byte("new\n"), 0o644); err != nil {
			t.Fatalf("write new file failed: %v", err)
		}

		tree2, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree changed failed: %v", err)
		}
		ref2, commit2, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree2, "")
		if err != nil {
			t.Fatalf("create checkpoint changed failed: %v", err)
		}

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"show", "--git-diff", "--name-only", ref2})

		if err := root.Execute(); err != nil {
			t.Fatalf("show --git-diff --name-only failed: %v", err)
		}

		output := buf.String()
		want := runGitOutput(t, repo, "diff", "--no-color", "--name-only", ref1, commit2)
		if strings.TrimSpace(output) != want {
			t.Fatalf("expected exact git diff --name-only output\nwant:\n%s\n\ngot:\n%s", want, output)
		}
		for _, forbidden := range []string{"checkpoint:", "commit:", "timestamp:", "status:", "check:", "diff --git"} {
			if strings.Contains(output, forbidden) {
				t.Fatalf("expected --git-diff --name-only output without %q, got: %q", forbidden, output)
			}
		}
		if !strings.Contains(output, "file.txt") {
			t.Fatalf("expected changed file name in output, got: %q", output)
		}
		if !strings.Contains(output, "new.txt") {
			t.Fatalf("expected new file name in output, got: %q", output)
		}
	})
}

func TestShowCommandShowsInclusiveCheckpointRange(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ref1, ref2, ref3 := createCheckpointRangeScenario(t, repo)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"show", "first+1..last"})

		if err := root.Execute(); err != nil {
			t.Fatalf("show checkpoint range failed: %v", err)
		}

		output := buf.String()
		for _, want := range []string{
			"checkpoint range: " + ref2 + ".." + ref3,
			"start checkpoint: " + ref2,
			"end checkpoint: " + ref3,
			"end commit:",
			"end status:",
			"+good checkpoint 3",
		} {
			if !strings.Contains(output, want) {
				t.Fatalf("expected range show output to contain %q, got: %q", want, output)
			}
		}
		if strings.Contains(output, "bad checkpoint 2") {
			t.Fatalf("expected range show to display net patch from %s through %s, got canceled intermediate content: %q", ref1, ref3, output)
		}
	})
}

func TestShowCommandGitDiffShowsExactGitDiffOutputForRange(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ref1, _, ref3 := createCheckpointRangeScenario(t, repo)
		commit3, err := resolveAutosnapRefToCommit(context.Background(), repo, ref3)
		if err != nil {
			t.Fatalf("resolve third checkpoint commit failed: %v", err)
		}

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"show", "--git-diff", "first+1..last"})

		if err := root.Execute(); err != nil {
			t.Fatalf("show --git-diff checkpoint range failed: %v", err)
		}

		output := buf.String()
		want := runGitOutput(t, repo, "diff", "--no-color", ref1, commit3)
		if strings.TrimSpace(output) != want {
			t.Fatalf("expected exact git diff output for range\nwant:\n%s\n\ngot:\n%s", want, output)
		}
		for _, forbidden := range []string{"checkpoint range:", "start checkpoint:", "end checkpoint:", "end commit:", "end timestamp:", "end status:", "end check:"} {
			if strings.Contains(output, forbidden) {
				t.Fatalf("expected --git-diff range output without %q metadata, got: %q", forbidden, output)
			}
		}
	})
}

func TestShowCommandRejectsMalformedRange(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		root := NewRootCommand()
		root.SetArgs([]string{"show", "first..last..last"})

		err := root.Execute()
		if err == nil {
			t.Fatalf("expected malformed checkpoint range to fail")
		}
		if !strings.Contains(err.Error(), "invalid checkpoint range") {
			t.Fatalf("expected invalid checkpoint range error, got %v", err)
		}
	})
}

func TestShowCommandUsesPreviousCheckpointForDiffBase(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		originalNow := currentTimestampFn
		currentTimestampFn = func() string { return "20260101T120000Z" }
		defer func() { currentTimestampFn = originalNow }()

		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("checkpoint base\n"), 0o644); err != nil {
			t.Fatalf("write first checkpoint failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		ref1, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "")
		if err != nil {
			t.Fatalf("create first checkpoint failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("intermediate\n"), 0o644); err != nil {
			t.Fatalf("update file for intermediate commit failed: %v", err)
		}
		runGit(t, repo, "add", "file.txt")
		runGit(t, repo, "commit", "-m", "intermediate commit")

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("checkpointed\n"), 0o644); err != nil {
			t.Fatalf("update file for second checkpoint failed: %v", err)
		}

		tree2, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree for second checkpoint failed: %v", err)
		}
		ref2, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree2, "")
		if err != nil {
			t.Fatalf("create second checkpoint failed: %v", err)
		}
		if ref1 == ref2 {
			t.Fatalf("expected unique checkpoint refs for timestamp collision, got duplicate %q", ref2)
		}

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"show", ref2})

		if err := root.Execute(); err != nil {
			t.Fatalf("show with previous checkpoint diff base failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "+checkpointed") {
			t.Fatalf("expected checkpointed patch in output, got: %q", output)
		}
		parsedTs, err := time.Parse("20060102T150405Z", "20260101T120000Z")
		if err != nil {
			t.Fatalf("parse timestamp failed: %v", err)
		}
		want := parsedTs.Local().Format("2006-01-02 15:04:05 MST")
		if !strings.Contains(output, "timestamp: "+want) {
			t.Fatalf("expected formatted show timestamp, got: %q", output)
		}
		if strings.Contains(output, "timestamp: 20260101T120000Z.") {
			t.Fatalf("expected timestamp to omit collision suffix in show output: %q", output)
		}
		if strings.Contains(output, "-intermediate") {
			t.Fatalf("expected diff to compare against previous checkpoint, got output containing intermediate: %q", output)
		}
	})
}

func TestShowCommandUsesTargetCheckpointBranchForExplicitRefDiffBase(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		originalNow := currentTimestampFn
		currentTimestampFn = func() string { return "20260102T120000Z" }
		defer func() { currentTimestampFn = originalNow }()

		_, _, mainBranchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}

		runGit(t, repo, "checkout", "-b", "feature/explicit-ref")
		_, _, featureBranchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository on feature branch failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("checkpoint one\n"), 0o644); err != nil {
			t.Fatalf("write feature checkpoint one failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree for checkpoint one failed: %v", err)
		}
		ref1, _, err := createCheckpoint(context.Background(), repo, featureBranchRef, "npm test", 5*time.Second, tree, "")
		if err != nil {
			t.Fatalf("create feature checkpoint one failed: %v", err)
		}
		runGit(t, repo, "reset", "--hard", "HEAD")

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("checkpoint two\n"), 0o644); err != nil {
			t.Fatalf("write feature checkpoint two failed: %v", err)
		}
		tree2, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree for checkpoint two failed: %v", err)
		}
		ref2, _, err := createCheckpoint(context.Background(), repo, featureBranchRef, "npm test", 5*time.Second, tree2, "")
		if err != nil {
			t.Fatalf("create feature checkpoint two failed: %v", err)
		}
		if ref1 == ref2 {
			t.Fatalf("expected unique checkpoint refs for timestamp collision, got duplicate %q", ref2)
		}
		runGit(t, repo, "reset", "--hard", "HEAD")
		runGit(t, repo, "checkout", mainBranchRef)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"show", ref2})

		if err := root.Execute(); err != nil {
			t.Fatalf("show explicit ref failed: %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "-checkpoint one") {
			t.Fatalf("expected previous checkpoint diff context, missing removed line; got: %q", output)
		}
		if !strings.Contains(output, "+checkpoint two") {
			t.Fatalf("expected explicit ref diff output for latest checkpoint, missing added line; got: %q", output)
		}
	})
}

func TestShowCommandColorModes(t *testing.T) {
	t.Parallel()
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

		fullArgs := []string{"show", "--color=always", ref2}
		colorBuf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(colorBuf)
		root.SetErr(colorBuf)
		root.SetArgs(fullArgs)

		if err := root.Execute(); err != nil {
			t.Fatalf("show --color=always failed: %v", err)
		}
		if !strings.Contains(colorBuf.String(), "\x1b[") {
			t.Fatalf("expected ANSI color escapes for --color=always, got: %q", colorBuf.String())
		}

		autoArgs := []string{"show", ref2}
		autoBuf := &bytes.Buffer{}
		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newShowCommand())
		root.SetOut(autoBuf)
		root.SetErr(autoBuf)
		root.SetArgs(autoArgs)

		if err := root.Execute(); err != nil {
			t.Fatalf("show (auto) failed: %v", err)
		}
		if strings.Contains(autoBuf.String(), "\x1b[") {
			t.Fatalf("expected auto mode to avoid ANSI when output is not a terminal: %q", autoBuf.String())
		}
	})
}

func TestRestoreCommandAppliesCheckpointToWorktreeAndIndex(t *testing.T) {
	t.Parallel()
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
		_, checkpointCommit, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "restore me")
		if err != nil {
			t.Fatalf("create checkpoint failed: %v", err)
		}
		runGit(t, repo, "reset", "--hard", "HEAD")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newRestoreCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"restore", checkpointCommit[:7]})

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
	t.Parallel()
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
		_, checkpointCommit, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "")
		if err != nil {
			t.Fatalf("create checkpoint failed: %v", err)
		}
		runGit(t, repo, "reset", "--hard", "HEAD")
		if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatalf("write dirty file failed: %v", err)
		}

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newRestoreCommand())
		root.SetArgs([]string{"restore", checkpointCommit[:7]})

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
	t.Parallel()
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
		_, checkpointCommit, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "")
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
		root.SetArgs([]string{"restore", "--force", checkpointCommit[:7]})

		err = root.Execute()
		if err == nil {
			t.Fatalf("expected restore to report merge conflict")
		}
		msg := err.Error()
		if !strings.Contains(msg, "checkpoint restored with conflicts") {
			t.Fatalf("expected restored-with-conflicts message, got %v", err)
		}
		for _, want := range []string{"Conflicted paths:", "file.txt", "git mergetool", "git reset --hard"} {
			if !strings.Contains(msg, want) {
				t.Fatalf("expected conflict guidance to contain %q, got %v", want, err)
			}
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

func TestRootCommandIncludesPickCommand(t *testing.T) {
	root := NewRootCommand()
	for _, command := range root.Commands() {
		if command.Name() == "pick" {
			return
		}
	}
	t.Fatalf("expected root command to include pick")
}

func TestRootCommandIncludesUnpickCommand(t *testing.T) {
	root := NewRootCommand()
	for _, command := range root.Commands() {
		if command.Name() == "unpick" {
			return
		}
	}
	t.Fatalf("expected root command to include unpick")
}

func TestPickCommandAppliesIncrementalCheckpointPatch(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		originalNow := currentTimestampFn
		timestamps := []string{"20260101T120000Z", "20260101T120001Z", "20260101T120002Z"}
		i := 0
		currentTimestampFn = func() string {
			value := timestamps[i]
			i++
			return value
		}
		defer func() { currentTimestampFn = originalNow }()

		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("checkpoint 1\n"), 0o644); err != nil {
			t.Fatalf("write first checkpoint file failed: %v", err)
		}
		tree1, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree first failed: %v", err)
		}
		ref1, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree1, "first")
		if err != nil {
			t.Fatalf("create first checkpoint failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "bad.txt"), []byte("bad checkpoint 2\n"), 0o644); err != nil {
			t.Fatalf("write bad checkpoint file failed: %v", err)
		}
		tree2, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree second failed: %v", err)
		}
		if _, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree2, "second"); err != nil {
			t.Fatalf("create second checkpoint failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "good.txt"), []byte("good checkpoint 3\n"), 0o644); err != nil {
			t.Fatalf("write good checkpoint file failed: %v", err)
		}
		tree3, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree third failed: %v", err)
		}
		ref3, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree3, "third")
		if err != nil {
			t.Fatalf("create third checkpoint failed: %v", err)
		}

		runGit(t, repo, "reset", "--hard", "HEAD")
		runGit(t, repo, "clean", "-fd")

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPromoteCommand())
		root.AddCommand(newPickCommand())
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"promote", ref1})
		if err := root.Execute(); err != nil {
			t.Fatalf("promote first checkpoint failed: %v", err)
		}

		root.SetArgs([]string{"pick", ref3})
		if err := root.Execute(); err != nil {
			t.Fatalf("pick third checkpoint failed: %v", err)
		}

		if _, err := os.Stat(filepath.Join(repo, "bad.txt")); !os.IsNotExist(err) {
			t.Fatalf("expected bad.txt from checkpoint 2 to be absent after picking checkpoint 3, stat err=%v", err)
		}
		if got := runGitOutput(t, repo, "show", ":good.txt"); got != "good checkpoint 3" {
			t.Fatalf("expected good.txt to be staged from checkpoint 3, got %q", got)
		}
		if status := runGitOutput(t, repo, "status", "--porcelain"); !strings.Contains(status, "A  good.txt") {
			t.Fatalf("expected picked change to be staged, got %q", status)
		}
	})
}

func TestPickCommandAppliesInclusiveCheckpointRange(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ref1, _, ref3 := createCheckpointRangeScenario(t, repo)

		runGit(t, repo, "reset", "--hard", "HEAD")
		runGit(t, repo, "clean", "-fd")

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPromoteCommand())
		root.AddCommand(newPickCommand())
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"promote", ref1})
		if err := root.Execute(); err != nil {
			t.Fatalf("promote first checkpoint failed: %v", err)
		}

		root.SetArgs([]string{"pick", "first+1..last"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pick checkpoint range failed: %v", err)
		}

		if _, err := os.Stat(filepath.Join(repo, "bad.txt")); !os.IsNotExist(err) {
			t.Fatalf("expected canceled bad.txt to be absent after range pick through %s, stat err=%v", ref3, err)
		}
		if got := runGitOutput(t, repo, "show", ":good.txt"); got != "good checkpoint 3" {
			t.Fatalf("expected good.txt to be staged from range pick, got %q", got)
		}
		if status := runGitOutput(t, repo, "status", "--porcelain"); !strings.Contains(status, "A  good.txt") {
			t.Fatalf("expected range picked change to be staged, got %q", status)
		}
	})
}

func TestPickCommandFirstCheckpointMatchesRestoreBase(t *testing.T) {
	t.Parallel()
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

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("first checkpoint\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint file failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		_, commit, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "first")
		if err != nil {
			t.Fatalf("create checkpoint failed: %v", err)
		}
		runGit(t, repo, "reset", "--hard", "HEAD")

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPickCommand())
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"pick", commit[:7]})
		if err := root.Execute(); err != nil {
			t.Fatalf("pick first checkpoint failed: %v", err)
		}

		if got := runGitOutput(t, repo, "show", ":file.txt"); got != "first checkpoint" {
			t.Fatalf("expected first checkpoint content to be staged, got %q", got)
		}
		if status := runGitOutput(t, repo, "status", "--porcelain"); !strings.Contains(status, "M  file.txt") {
			t.Fatalf("expected first checkpoint pick to stage modified file, got %q", status)
		}
	})
}

func TestPickCommandRejectsTimestamp(t *testing.T) {
	t.Parallel()
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
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("pick timestamp checkpoint\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		ref, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "")
		if err != nil {
			t.Fatalf("create checkpoint failed: %v", err)
		}

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPickCommand())
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"pick", path.Base(ref)})

		err = root.Execute()
		if err == nil {
			t.Fatalf("expected pick to reject timestamp identifier %q", path.Base(ref))
		}
		if !strings.Contains(err.Error(), "checkpoint identifier") && !strings.Contains(err.Error(), "checkpoint not found") {
			t.Fatalf("expected rejection for unsupported pick argument, got: %v", err)
		}
	})
}

func TestPickCommandRefusesDirtyStateByDefault(t *testing.T) {
	t.Parallel()
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
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("pick checkpoint\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		_, commit, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "")
		if err != nil {
			t.Fatalf("create checkpoint failed: %v", err)
		}
		runGit(t, repo, "reset", "--hard", "HEAD")
		if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("dirty\n"), 0o644); err != nil {
			t.Fatalf("write dirty file failed: %v", err)
		}

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPickCommand())
		root.SetArgs([]string{"pick", commit[:7]})

		err = root.Execute()
		if err == nil {
			t.Fatalf("expected pick to refuse dirty worktree")
		}
		if !strings.Contains(err.Error(), "clean worktree") {
			t.Fatalf("expected clean worktree error, got %v", err)
		}
	})
}

func TestPickCommandReportsAppliedConflictsWithoutUsage(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		originalNow := currentTimestampFn
		timestamps := []string{"20260101T120000Z", "20260101T120001Z"}
		i := 0
		currentTimestampFn = func() string {
			value := timestamps[i]
			i++
			return value
		}
		defer func() { currentTimestampFn = originalNow }()

		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		gitDirectory, err := gitDir(context.Background(), repo)
		if err != nil {
			t.Fatalf("gitDir failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("checkpoint base\n"), 0o644); err != nil {
			t.Fatalf("write first checkpoint failed: %v", err)
		}
		tree1, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree first failed: %v", err)
		}
		if _, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree1, "first"); err != nil {
			t.Fatalf("create first checkpoint failed: %v", err)
		}

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("picked checkpoint\n"), 0o644); err != nil {
			t.Fatalf("write second checkpoint failed: %v", err)
		}
		tree2, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree second failed: %v", err)
		}
		ref2, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree2, "second")
		if err != nil {
			t.Fatalf("create second checkpoint failed: %v", err)
		}

		runGit(t, repo, "reset", "--hard", "HEAD")
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("conflicting branch\n"), 0o644); err != nil {
			t.Fatalf("write conflicting branch file failed: %v", err)
		}
		runGit(t, repo, "add", "file.txt")
		runGit(t, repo, "commit", "-m", "conflicting branch commit")

		buf := &bytes.Buffer{}
		root := NewRootCommand()
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pick", "--force", ref2})

		err = root.Execute()
		if err == nil {
			t.Fatalf("expected pick to report conflicts")
		}
		msg := err.Error()
		for _, want := range []string{
			"checkpoint picked with conflicts: " + ref2,
			"Conflicted paths:",
			"file.txt",
			"git mergetool",
			"git reset --hard",
		} {
			if !strings.Contains(msg, want) {
				t.Fatalf("expected pick conflict message to contain %q, got %v", want, err)
			}
		}
		if strings.Contains(msg, "failed to pick") {
			t.Fatalf("expected conflict message not to use failed-to-pick framing, got %v", err)
		}
		if strings.Contains(buf.String(), "Usage:") {
			t.Fatalf("expected runtime conflict not to print usage, got %q", buf.String())
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

func TestPickCommandConflictCheckpointPolicyUsesCheckpointSide(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ref := createPickConflictScenario(t, repo)

		buf := &bytes.Buffer{}
		root := NewRootCommand()
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pick", "--force", "--conflict=checkpoint", ref})

		if err := root.Execute(); err != nil {
			t.Fatalf("pick --conflict=checkpoint failed: %v", err)
		}

		if got := runGitOutput(t, repo, "show", ":file.txt"); got != "picked checkpoint" {
			t.Fatalf("expected checkpoint side to be staged, got %q", got)
		}
		if got := runGitOutput(t, repo, "status", "--porcelain"); !strings.Contains(got, "M  file.txt") {
			t.Fatalf("expected checkpoint-side resolution to be staged, got %q", got)
		}
		if strings.Contains(buf.String(), "with conflicts") {
			t.Fatalf("expected resolved conflict not to print conflict guidance, got %q", buf.String())
		}
	})
}

func TestPickCommandConflictHeadPolicyUsesHeadSide(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ref := createPickConflictScenario(t, repo)

		buf := &bytes.Buffer{}
		root := NewRootCommand()
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"pick", "--force", "--conflict=head", ref})

		if err := root.Execute(); err != nil {
			t.Fatalf("pick --conflict=head failed: %v", err)
		}

		if got := runGitOutput(t, repo, "show", "HEAD:file.txt"); got != "conflicting branch" {
			t.Fatalf("expected HEAD content to remain unchanged, got %q", got)
		}
		if got := runGitOutput(t, repo, "status", "--porcelain"); got != "" {
			t.Fatalf("expected HEAD-side resolution to leave no staged change, got %q", got)
		}
		if strings.Contains(buf.String(), "with conflicts") {
			t.Fatalf("expected resolved conflict not to print conflict guidance, got %q", buf.String())
		}
	})
}

func TestPickCommandRejectsInvalidConflictPolicy(t *testing.T) {
	t.Parallel()
	root := NewRootCommand()
	root.SetArgs([]string{"pick", "--conflict=invalid", "last"})

	err := root.Execute()
	if err == nil {
		t.Fatalf("expected invalid conflict policy to fail")
	}
	if !strings.Contains(err.Error(), "invalid --conflict value") {
		t.Fatalf("expected invalid conflict policy error, got %v", err)
	}
}

func TestPickCommandRejectsMalformedRange(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		root := NewRootCommand()
		root.SetArgs([]string{"pick", "first..last..last"})

		err := root.Execute()
		if err == nil {
			t.Fatalf("expected malformed checkpoint range to fail")
		}
		if !strings.Contains(err.Error(), "invalid checkpoint range") {
			t.Fatalf("expected invalid checkpoint range error, got %v", err)
		}
	})
}

func TestUnpickCommandRemovesIncrementalCheckpointPatch(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ref1, ref2, _ := createCheckpointRangeScenario(t, repo)

		runGit(t, repo, "reset", "--hard", "HEAD")
		runGit(t, repo, "clean", "-fd")

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPromoteCommand())
		root.AddCommand(newPickCommand())
		root.AddCommand(newUnpickCommand())
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"promote", ref1})
		if err := root.Execute(); err != nil {
			t.Fatalf("promote first checkpoint failed: %v", err)
		}

		root.SetArgs([]string{"pick", ref2})
		if err := root.Execute(); err != nil {
			t.Fatalf("pick second checkpoint failed: %v", err)
		}
		if got := runGitOutput(t, repo, "show", ":bad.txt"); got != "bad checkpoint 2" {
			t.Fatalf("expected bad.txt to be staged by pick, got %q", got)
		}

		root.SetArgs([]string{"unpick", "--force", ref2})
		if err := root.Execute(); err != nil {
			t.Fatalf("unpick second checkpoint failed: %v", err)
		}

		if _, err := os.Stat(filepath.Join(repo, "bad.txt")); !os.IsNotExist(err) {
			t.Fatalf("expected bad.txt to be removed by unpick, stat err=%v", err)
		}
		if status := runGitOutput(t, repo, "status", "--porcelain"); status != "" {
			t.Fatalf("expected unpick to restore clean promoted checkpoint state, got %q", status)
		}
	})
}

func TestUnpickCommandRemovesInclusiveCheckpointRange(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		ref1, _, ref3 := createCheckpointRangeScenario(t, repo)

		runGit(t, repo, "reset", "--hard", "HEAD")
		runGit(t, repo, "clean", "-fd")

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPromoteCommand())
		root.AddCommand(newPickCommand())
		root.AddCommand(newUnpickCommand())
		root.SetOut(&bytes.Buffer{})
		root.SetErr(&bytes.Buffer{})
		root.SetArgs([]string{"promote", ref1})
		if err := root.Execute(); err != nil {
			t.Fatalf("promote first checkpoint failed: %v", err)
		}

		root.SetArgs([]string{"pick", "first+1..last"})
		if err := root.Execute(); err != nil {
			t.Fatalf("pick checkpoint range failed: %v", err)
		}
		if got := runGitOutput(t, repo, "show", ":good.txt"); got != "good checkpoint 3" {
			t.Fatalf("expected good.txt to be staged by range pick, got %q", got)
		}

		root.SetArgs([]string{"unpick", "--force", "first+1..last"})
		if err := root.Execute(); err != nil {
			t.Fatalf("unpick checkpoint range through %s failed: %v", ref3, err)
		}

		if _, err := os.Stat(filepath.Join(repo, "good.txt")); !os.IsNotExist(err) {
			t.Fatalf("expected good.txt to be removed by range unpick, stat err=%v", err)
		}
		if _, err := os.Stat(filepath.Join(repo, "bad.txt")); !os.IsNotExist(err) {
			t.Fatalf("expected canceled bad.txt to remain absent after range unpick, stat err=%v", err)
		}
		if status := runGitOutput(t, repo, "status", "--porcelain"); status != "" {
			t.Fatalf("expected range unpick to restore clean promoted checkpoint state, got %q", status)
		}
	})
}

func TestUnpickCommandRejectsInvalidConflictPolicy(t *testing.T) {
	t.Parallel()
	root := NewRootCommand()
	root.SetArgs([]string{"unpick", "--conflict=checkpoint", "last"})

	err := root.Execute()
	if err == nil {
		t.Fatalf("expected invalid conflict policy to fail")
	}
	if !strings.Contains(err.Error(), "invalid --conflict value") {
		t.Fatalf("expected invalid conflict policy error, got %v", err)
	}
}

func TestUnpickCommandRejectsReversedRange(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		createCheckpointRangeScenario(t, repo)

		root := NewRootCommand()
		root.SetArgs([]string{"unpick", "last..first+1"})

		err := root.Execute()
		if err == nil {
			t.Fatalf("expected reversed checkpoint range to fail")
		}
		if !strings.Contains(err.Error(), "range start must not be after range end") {
			t.Fatalf("expected reversed range error, got %v", err)
		}
	})
}

func TestPromoteCommandRejectsTimestamp(t *testing.T) {
	t.Parallel()
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
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("promote timestamp checkpoint\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint failed: %v", err)
		}
		tree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
		if err != nil {
			t.Fatalf("computeWorktreeTree failed: %v", err)
		}
		ref, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, "timestamp test")
		if err != nil {
			t.Fatalf("create checkpoint failed: %v", err)
		}

		timestamp := path.Base(ref)
		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPromoteCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"promote", timestamp})

		err = root.Execute()
		if err == nil {
			t.Fatalf("expected promote to reject timestamp identifier %q", timestamp)
		}
		if !strings.Contains(err.Error(), "checkpoint identifier") && !strings.Contains(err.Error(), "checkpoint not found") {
			t.Fatalf("expected rejection for unsupported promote argument, got: %v", err)
		}
		if strings.Contains(err.Error(), ref) {
			t.Fatalf("expected no successful promote output, got: %q", err.Error())
		}
	})
}

func TestPromoteCommandCreatesBranchCommitFromCheckpoint(t *testing.T) {
	t.Parallel()
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
		_, checkpointCommit, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree, message)
		if err != nil {
			t.Fatalf("create checkpoint failed: %v", err)
		}
		runGit(t, repo, "reset", "--hard", "HEAD")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPromoteCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"promote", checkpointCommit[:7]})

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
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		oldHead := runGitOutput(t, repo, "rev-parse", "HEAD")

		checkpointRef := createAutosnapTestCommitRef(t, repo, branchRef, "20210108T000000Z", "matching checkpoint")
		checkpointCommit := runGitOutput(t, repo, "rev-parse", checkpointRef)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPromoteCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"promote", checkpointCommit[:7]})

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
	t.Parallel()
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

func TestBranchCreateCopiesCurrentBranchCheckpointRefs(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, sourceBranch, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		first := createAutosnapTestCommitRef(t, repo, sourceBranch, "20260101T000000Z", "first checkpoint")
		second := createAutosnapTestCommitRef(t, repo, sourceBranch, "20260101T000001Z", "second checkpoint")
		firstCommit := runGitOutput(t, repo, "rev-parse", first)
		secondCommit := runGitOutput(t, repo, "rev-parse", second)

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newBranchCommand())
		root.SetOut(buf)
		root.SetArgs([]string{"branch", "create", "feature/next"})

		if err := root.Execute(); err != nil {
			t.Fatalf("branch create failed: %v", err)
		}
		if got := runGitOutput(t, repo, "branch", "--show-current"); got != "feature/next" {
			t.Fatalf("expected checked-out branch feature/next, got %q", got)
		}
		targetFirst := snapshotRef("feature/next", "20260101T000000Z")
		targetSecond := snapshotRef("feature/next", "20260101T000001Z")
		if got := runGitOutput(t, repo, "rev-parse", targetFirst); got != firstCommit {
			t.Fatalf("expected copied first commit %s, got %s", firstCommit, got)
		}
		if got := runGitOutput(t, repo, "rev-parse", targetSecond); got != secondCommit {
			t.Fatalf("expected copied second commit %s, got %s", secondCommit, got)
		}
		if !strings.Contains(buf.String(), "created and checked out branch feature/next") ||
			!strings.Contains(buf.String(), "copied 2 checkpoint ref(s) from "+sourceBranch+" to feature/next") {
			t.Fatalf("unexpected branch create output: %q", buf.String())
		}
	})
}

func TestBranchCreateNoCopyCheckpoints(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, sourceBranch, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		createAutosnapTestCommitRef(t, repo, sourceBranch, "20260101T000000Z", "first checkpoint")

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newBranchCommand())
		root.SetArgs([]string{"branch", "create", "feature/no-copy", "--no-copy-checkpoints"})

		if err := root.Execute(); err != nil {
			t.Fatalf("branch create --no-copy-checkpoints failed: %v", err)
		}
		if got := runGitOutput(t, repo, "branch", "--show-current"); got != "feature/no-copy" {
			t.Fatalf("expected checked-out branch feature/no-copy, got %q", got)
		}
		if gitRefExists(t, repo, snapshotRef("feature/no-copy", "20260101T000000Z")) {
			t.Fatalf("expected no checkpoint ref to be copied")
		}
	})
}

func TestBranchCreateRefusesCheckpointCollisionsBeforeCheckout(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, sourceBranch, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		sourceRef := createAutosnapTestCommitRef(t, repo, sourceBranch, "20260101T000000Z", "source checkpoint")
		sourceCommit := runGitOutput(t, repo, "rev-parse", sourceRef)
		targetRef := createAutosnapTestCommitRef(t, repo, "feature/preexisting", "20260101T000000Z", "target checkpoint")
		targetCommit := runGitOutput(t, repo, "rev-parse", targetRef)

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newBranchCommand())
		root.SetArgs([]string{"branch", "create", "feature/preexisting"})

		err = root.Execute()
		if err == nil || !strings.Contains(err.Error(), "use --overwrite to replace colliding refs") {
			t.Fatalf("expected collision error, got %v", err)
		}
		if got := runGitOutput(t, repo, "branch", "--show-current"); got != sourceBranch {
			t.Fatalf("branch create collision should not change checkout; got %q", got)
		}
		if gitRefExists(t, repo, "refs/heads/feature/preexisting") {
			t.Fatalf("branch create collision should not create target Git branch")
		}
		if got := runGitOutput(t, repo, "rev-parse", targetRef); got != targetCommit {
			t.Fatalf("expected target ref to remain %s, got %s", targetCommit, got)
		}

		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newBranchCommand())
		root.SetArgs([]string{"branch", "create", "feature/preexisting", "--overwrite"})

		if err := root.Execute(); err != nil {
			t.Fatalf("branch create --overwrite failed: %v", err)
		}
		if got := runGitOutput(t, repo, "branch", "--show-current"); got != "feature/preexisting" {
			t.Fatalf("expected checked-out branch feature/preexisting, got %q", got)
		}
		if got := runGitOutput(t, repo, "rev-parse", targetRef); got != sourceCommit {
			t.Fatalf("expected target ref to be overwritten with %s, got %s", sourceCommit, got)
		}
	})
}

func TestBranchCopyCopiesRefsWithoutCheckout(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, sourceBranch, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		sourceRef := createAutosnapTestCommitRef(t, repo, sourceBranch, "20260101T000000Z", "source checkpoint")
		sourceCommit := runGitOutput(t, repo, "rev-parse", sourceRef)
		runGit(t, repo, "branch", "feature/copy-target")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newBranchCommand())
		root.SetOut(buf)
		root.SetArgs([]string{"branch", "copy", "--from", sourceBranch, "--to", "feature/copy-target"})

		if err := root.Execute(); err != nil {
			t.Fatalf("branch copy failed: %v", err)
		}
		if got := runGitOutput(t, repo, "branch", "--show-current"); got != sourceBranch {
			t.Fatalf("branch copy should not change checkout; got %q", got)
		}
		targetRef := snapshotRef("feature/copy-target", "20260101T000000Z")
		if got := runGitOutput(t, repo, "rev-parse", targetRef); got != sourceCommit {
			t.Fatalf("expected copied commit %s, got %s", sourceCommit, got)
		}
		if !strings.Contains(buf.String(), "copied 1 checkpoint ref(s) from "+sourceBranch+" to feature/copy-target") {
			t.Fatalf("unexpected branch copy output: %q", buf.String())
		}
	})
}

func TestBranchCopyRefusesMissingTargetGitBranch(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, sourceBranch, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newBranchCommand())
		root.SetArgs([]string{"branch", "copy", "--from", sourceBranch, "--to", "feature/missing"})

		err = root.Execute()
		if err == nil || !strings.Contains(err.Error(), "target Git branch does not exist: feature/missing") {
			t.Fatalf("expected missing target branch error, got %v", err)
		}
	})
}

func TestBranchCopyRefusesCollisionsUnlessOverwrite(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, sourceBranch, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		sourceRef := createAutosnapTestCommitRef(t, repo, sourceBranch, "20260101T000000Z", "source checkpoint")
		sourceCommit := runGitOutput(t, repo, "rev-parse", sourceRef)
		runGit(t, repo, "branch", "feature/collision")
		targetRef := createAutosnapTestCommitRef(t, repo, "feature/collision", "20260101T000000Z", "target checkpoint")
		targetCommit := runGitOutput(t, repo, "rev-parse", targetRef)

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newBranchCommand())
		root.SetArgs([]string{"branch", "copy", "--from", sourceBranch, "--to", "feature/collision"})

		err = root.Execute()
		if err == nil || !strings.Contains(err.Error(), "use --overwrite to replace colliding refs") {
			t.Fatalf("expected collision error, got %v", err)
		}
		if got := runGitOutput(t, repo, "rev-parse", targetRef); got != targetCommit {
			t.Fatalf("expected target ref to remain %s, got %s", targetCommit, got)
		}

		root = &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newBranchCommand())
		root.SetArgs([]string{"branch", "copy", "--from", sourceBranch, "--to", "feature/collision", "--overwrite"})

		if err := root.Execute(); err != nil {
			t.Fatalf("branch copy --overwrite failed: %v", err)
		}
		if got := runGitOutput(t, repo, "rev-parse", targetRef); got != sourceCommit {
			t.Fatalf("expected target ref to be overwritten with %s, got %s", sourceCommit, got)
		}
	})
}

func TestPruneCommandRejectsInvalidScopeAndPolicy(t *testing.T) {
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
		parsedOldest, err := time.Parse("20060102T150405Z", "20200101T000000Z")
		if err != nil {
			t.Fatalf("parse timestamp failed: %v", err)
		}
		wantOldest := parsedOldest.Local().Format("2006-01-02 15:04:05 MST")
		parsedMiddle, err := time.Parse("20060102T150405Z", "20210101T000000Z")
		if err != nil {
			t.Fatalf("parse timestamp failed: %v", err)
		}
		wantMiddle := parsedMiddle.Local().Format("2006-01-02 15:04:05 MST")

		if !strings.Contains(output, "dry run: 2 checkpoint(s) would be pruned") {
			t.Fatalf("expected dry-run count, got %q", output)
		}
		if !strings.Contains(output, oldest) || !strings.Contains(output, middle) {
			t.Fatalf("expected old refs in dry-run output, got %q", output)
		}
		if !strings.Contains(output, wantOldest) || !strings.Contains(output, wantMiddle) {
			t.Fatalf("expected formatted timestamps in prune output, got %q", output)
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

func TestWorktreeMarkerPathUpdates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		entries      []porcelainStatusEntry
		wantAdd      []string
		wantRemove   []string
		wantFallback bool
	}{
		{
			name:    "untracked",
			entries: []porcelainStatusEntry{{status: "??", path: "new.txt"}},
			wantAdd: []string{"new.txt"},
		},
		{
			name:    "unstaged modification",
			entries: []porcelainStatusEntry{{status: " M", path: "file.txt"}},
			wantAdd: []string{"file.txt"},
		},
		{
			name:       "unstaged deletion",
			entries:    []porcelainStatusEntry{{status: " D", path: "file.txt"}},
			wantRemove: []string{"file.txt"},
		},
		{
			name:    "staged only",
			entries: []porcelainStatusEntry{{status: "M ", path: "file.txt"}},
		},
		{
			name:         "rename falls back",
			entries:      []porcelainStatusEntry{{status: "R ", path: "new.txt", previousPath: "old.txt"}},
			wantFallback: true,
		},
		{
			name:         "unmerged falls back",
			entries:      []porcelainStatusEntry{{status: "UU", path: "file.txt"}},
			wantFallback: true,
		},
		{
			name:         "unknown worktree status falls back",
			entries:      []porcelainStatusEntry{{status: " X", path: "file.txt"}},
			wantFallback: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			addPaths, removePaths, fallback := worktreeMarkerPathUpdates(tt.entries)
			if !reflect.DeepEqual(emptyStringSlice(addPaths), emptyStringSlice(tt.wantAdd)) {
				t.Fatalf("add paths = %#v, want %#v", addPaths, tt.wantAdd)
			}
			if !reflect.DeepEqual(emptyStringSlice(removePaths), emptyStringSlice(tt.wantRemove)) {
				t.Fatalf("remove paths = %#v, want %#v", removePaths, tt.wantRemove)
			}
			if fallback != tt.wantFallback {
				t.Fatalf("fallback = %t, want %t", fallback, tt.wantFallback)
			}
		})
	}
}

func emptyStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func TestComputeWorktreeMatchTreesMatchesExistingSnapshotModeTrees(t *testing.T) {
	t.Parallel()
	requireIntegration(t)

	tests := []struct {
		name  string
		setup func(t *testing.T, repo string)
	}{
		{
			name: "clean tree",
			setup: func(t *testing.T, repo string) {
				t.Helper()
			},
		},
		{
			name: "staged only change",
			setup: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("staged\n"), 0o644); err != nil {
					t.Fatalf("write staged file failed: %v", err)
				}
				runGit(t, repo, "add", "file.txt")
			},
		},
		{
			name: "unstaged tracked modification",
			setup: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("staged\n"), 0o644); err != nil {
					t.Fatalf("write staged file failed: %v", err)
				}
				runGit(t, repo, "add", "file.txt")
				if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("unstaged\n"), 0o644); err != nil {
					t.Fatalf("write unstaged file failed: %v", err)
				}
			},
		},
		{
			name: "tracked deletion",
			setup: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.Remove(filepath.Join(repo, "file.txt")); err != nil {
					t.Fatalf("remove tracked file failed: %v", err)
				}
			},
		},
		{
			name: "untracked file",
			setup: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("untracked\n"), 0o644); err != nil {
					t.Fatalf("write untracked file failed: %v", err)
				}
			},
		},
		{
			name: "ignored file",
			setup: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored.txt\n"), 0o644); err != nil {
					t.Fatalf("write gitignore failed: %v", err)
				}
				runGit(t, repo, "add", ".gitignore")
				if err := os.WriteFile(filepath.Join(repo, "ignored.txt"), []byte("ignored\n"), 0o644); err != nil {
					t.Fatalf("write ignored file failed: %v", err)
				}
			},
		},
		{
			name: "mixed modify delete untracked",
			setup: func(t *testing.T, repo string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("staged\n"), 0o644); err != nil {
					t.Fatalf("write staged file failed: %v", err)
				}
				runGit(t, repo, "add", "file.txt")
				if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("unstaged\n"), 0o644); err != nil {
					t.Fatalf("write unstaged file failed: %v", err)
				}
				if err := os.WriteFile(filepath.Join(repo, "delete-me.txt"), []byte("delete me\n"), 0o644); err != nil {
					t.Fatalf("write delete file failed: %v", err)
				}
				runGit(t, repo, "add", "delete-me.txt")
				if err := os.Remove(filepath.Join(repo, "delete-me.txt")); err != nil {
					t.Fatalf("remove delete file failed: %v", err)
				}
				if err := os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("untracked\n"), 0o644); err != nil {
					t.Fatalf("write untracked file failed: %v", err)
				}
			},
		},
		{
			name: "rename fallback",
			setup: func(t *testing.T, repo string) {
				t.Helper()
				runGit(t, repo, "mv", "file.txt", "renamed.txt")
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := createTestRepo(t)
			withWorkingDir(t, repo, func() {
				gitDirectory, err := gitDir(context.Background(), repo)
				if err != nil {
					t.Fatalf("gitDir failed: %v", err)
				}
				tt.setup(t, repo)

				wantWorktreeTree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
				if err != nil {
					t.Fatalf("computeWorktreeTree both failed: %v", err)
				}
				wantIndexTree, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeStaged)
				if err != nil {
					t.Fatalf("computeWorktreeTree staged failed: %v", err)
				}
				gotWorktreeTree, gotIndexTree, err := computeWorktreeMatchTrees(context.Background(), repo, gitDirectory)
				if err != nil {
					t.Fatalf("computeWorktreeMatchTrees failed: %v", err)
				}

				if gotWorktreeTree != wantWorktreeTree {
					t.Fatalf("worktree tree mismatch: got %s want %s", gotWorktreeTree, wantWorktreeTree)
				}
				if gotIndexTree != wantIndexTree {
					t.Fatalf("index tree mismatch: got %s want %s", gotIndexTree, wantIndexTree)
				}
			})
		})
	}
}

var testRepoTemplate struct {
	once sync.Once
	dir  string
	err  error
}

func createTestRepo(t *testing.T) string {
	t.Helper()
	testRepoTemplate.once.Do(func() {
		testRepoTemplate.dir, testRepoTemplate.err = createTestRepoTemplate()
	})
	if testRepoTemplate.err != nil {
		t.Fatalf("create test repo template failed: %v", testRepoTemplate.err)
	}

	dir := t.TempDir()
	if err := copyDir(testRepoTemplate.dir, dir); err != nil {
		t.Fatalf("copy test repo template failed: %v", err)
	}
	return dir
}

func createTestRepoTemplate() (string, error) {
	dir, err := os.MkdirTemp("", "autosnap-test-repo-template-")
	if err != nil {
		return "", err
	}
	sub := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		return "", err
	}
	cmds := [][]string{
		{"init", "-q"},
		{"config", "user.name", "Autosnap Test"},
		{"config", "user.email", "test@autosnap.local"},
	}
	for _, args := range cmds {
		if err := runGitTemplate(dir, args...); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("initial"), 0o644); err != nil {
		return "", err
	}
	if err := runGitTemplate(dir, "add", "file.txt"); err != nil {
		return "", err
	}
	if err := runGitTemplate(dir, "commit", "-m", "initial commit"); err != nil {
		return "", err
	}
	return dir, nil
}

func runGitTemplate(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, out)
	}
	return nil
}

func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, info.Mode().Perm()); err != nil {
				return err
			}
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
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

var testWorkingDirs sync.Map

func init() {
	detectRepositoryStartDir = func() string {
		if dir, ok := testWorkingDirs.Load(currentGoroutineID()); ok {
			return dir.(string)
		}
		return "."
	}
}

func withWorkingDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	gid := currentGoroutineID()
	testWorkingDirs.Store(gid, dir)
	defer testWorkingDirs.Delete(gid)
	fn()
}

func currentGoroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	fields := strings.Fields(string(buf[:n]))
	id, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		panic(fmt.Sprintf("parse goroutine id: %v", err))
	}
	return id
}

func createPickConflictScenario(t *testing.T, repo string) string {
	t.Helper()
	originalNow := currentTimestampFn
	timestamps := []string{"20260101T120000Z", "20260101T120001Z"}
	i := 0
	currentTimestampFn = func() string {
		value := timestamps[i]
		i++
		return value
	}
	t.Cleanup(func() { currentTimestampFn = originalNow })

	_, _, branchRef, err := detectRepository(context.Background())
	if err != nil {
		t.Fatalf("detectRepository failed: %v", err)
	}
	gitDirectory, err := gitDir(context.Background(), repo)
	if err != nil {
		t.Fatalf("gitDir failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("checkpoint base\n"), 0o644); err != nil {
		t.Fatalf("write first checkpoint failed: %v", err)
	}
	tree1, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
	if err != nil {
		t.Fatalf("computeWorktreeTree first failed: %v", err)
	}
	if _, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree1, "first"); err != nil {
		t.Fatalf("create first checkpoint failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("picked checkpoint\n"), 0o644); err != nil {
		t.Fatalf("write second checkpoint failed: %v", err)
	}
	tree2, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
	if err != nil {
		t.Fatalf("computeWorktreeTree second failed: %v", err)
	}
	ref2, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree2, "second")
	if err != nil {
		t.Fatalf("create second checkpoint failed: %v", err)
	}

	runGit(t, repo, "reset", "--hard", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("conflicting branch\n"), 0o644); err != nil {
		t.Fatalf("write conflicting branch file failed: %v", err)
	}
	runGit(t, repo, "add", "file.txt")
	runGit(t, repo, "commit", "-m", "conflicting branch commit")

	return ref2
}

func createCheckpointRangeScenario(t *testing.T, repo string) (string, string, string) {
	t.Helper()
	originalNow := currentTimestampFn
	timestamps := []string{"20260101T120000Z", "20260101T120001Z", "20260101T120002Z"}
	i := 0
	currentTimestampFn = func() string {
		value := timestamps[i]
		i++
		return value
	}
	t.Cleanup(func() { currentTimestampFn = originalNow })

	_, _, branchRef, err := detectRepository(context.Background())
	if err != nil {
		t.Fatalf("detectRepository failed: %v", err)
	}
	gitDirectory, err := gitDir(context.Background(), repo)
	if err != nil {
		t.Fatalf("gitDir failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("checkpoint 1\n"), 0o644); err != nil {
		t.Fatalf("write first checkpoint file failed: %v", err)
	}
	tree1, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
	if err != nil {
		t.Fatalf("computeWorktreeTree first failed: %v", err)
	}
	ref1, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree1, "first")
	if err != nil {
		t.Fatalf("create first checkpoint failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(repo, "bad.txt"), []byte("bad checkpoint 2\n"), 0o644); err != nil {
		t.Fatalf("write second checkpoint file failed: %v", err)
	}
	tree2, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
	if err != nil {
		t.Fatalf("computeWorktreeTree second failed: %v", err)
	}
	ref2, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree2, "second")
	if err != nil {
		t.Fatalf("create second checkpoint failed: %v", err)
	}

	if err := os.Remove(filepath.Join(repo, "bad.txt")); err != nil {
		t.Fatalf("remove bad checkpoint file failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "good.txt"), []byte("good checkpoint 3\n"), 0o644); err != nil {
		t.Fatalf("write third checkpoint file failed: %v", err)
	}
	tree3, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
	if err != nil {
		t.Fatalf("computeWorktreeTree third failed: %v", err)
	}
	ref3, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree3, "third")
	if err != nil {
		t.Fatalf("create third checkpoint failed: %v", err)
	}

	return ref1, ref2, ref3
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

func createAutosnapTestCommitRefFromIndex(t *testing.T, repoRoot, branchRef, timestamp, message string) string {
	t.Helper()
	tree := runGitOutput(t, repoRoot, "write-tree")
	return createAutosnapTestCommitRefFromTree(t, repoRoot, branchRef, timestamp, tree, message)
}

func createAutosnapTestCommitRefFromTree(t *testing.T, repoRoot, branchRef, timestamp, tree, message string) string {
	t.Helper()
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

func addGitNote(t *testing.T, repoRoot, noteRef, object, note string) {
	t.Helper()
	if _, err := runGitCommandWithInput(context.Background(), repoRoot, nil, note, "notes", "--ref", noteRef, "add", "-f", "-F", "-", object); err != nil {
		t.Fatalf("add git note failed: %v", err)
	}
}

func parseJSONLRows(t *testing.T, output string) []map[string]any {
	t.Helper()
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil
	}

	lines := strings.Split(trimmed, "\n")
	rows := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("parse JSONL line %q failed: %v", line, err)
		}
		rows = append(rows, row)
	}
	return rows
}

func parseJSONRows(t *testing.T, output string) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal([]byte(output), &rows); err != nil {
		t.Fatalf("parse JSON output %q failed: %v", output, err)
	}
	return rows
}

func lineContaining(output, needle string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

func stringPointer(value string) *string {
	return &value
}

func writeTestFiles(t *testing.T, repoRoot string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		fullPath := filepath.Join(repoRoot, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("create test file directory failed: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write test file failed: %v", err)
		}
	}
}

func jsonRowsByRef(rows []map[string]any) map[string]map[string]any {
	byRef := map[string]map[string]any{}
	for _, row := range rows {
		ref, _ := row["ref"].(string)
		byRef[ref] = row
	}
	return byRef
}

func gitRefExists(t *testing.T, repoRoot, ref string) bool {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--verify", ref)
	cmd.Dir = repoRoot
	return cmd.Run() == nil
}

func TestCheckpointCommandCreatesImmediateCheckpoint(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("manual checkpoint"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newCheckpointCommand())
		root.SetArgs([]string{"checkpoint", "--check", "true"})
		if err := root.Execute(); err != nil {
			t.Fatalf("checkpoint command failed: %v", err)
		}

		checkpoints, err := listCheckpointRefsForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("list checkpoints failed: %v", err)
		}
		if len(checkpoints) != 1 {
			t.Fatalf("expected one checkpoint, got %d", len(checkpoints))
		}
		if got := runGitOutput(t, repo, "show", checkpoints[0].Commit+":file.txt"); got != "manual checkpoint" {
			t.Fatalf("expected checkpoint content, got %q", got)
		}
	})
}

func TestCheckpointCommandUsesCommitMessageArgument(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("manual message checkpoint"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newCheckpointCommand())
		root.SetArgs([]string{"checkpoint", "manual checkpoint message", "--check", "true"})
		if err := root.Execute(); err != nil {
			t.Fatalf("checkpoint command failed: %v", err)
		}

		checkpoints, err := listCheckpointRefsForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("list checkpoints failed: %v", err)
		}
		if len(checkpoints) != 1 {
			t.Fatalf("expected one checkpoint, got %d", len(checkpoints))
		}
		if got := runGitOutput(t, repo, "log", "-1", "--pretty=%s", checkpoints[0].Commit); got != "manual checkpoint message" {
			t.Fatalf("expected checkpoint subject %q, got %q", "manual checkpoint message", got)
		}
	})
}

func TestCheckpointCommandRejectsCommitMessageArgumentWithMsgSourceCmd(t *testing.T) {
	t.Parallel()
	err := validateCheckpointCommitMessageSources("manual checkpoint message", "printf sourced")
	if err == nil {
		t.Fatalf("expected checkpoint command to reject conflicting message sources")
	}
	if !strings.Contains(err.Error(), "COMMIT_MSG cannot be used with --msg-source-cmd") {
		t.Fatalf("expected COMMIT_MSG conflict error, got %v", err)
	}
}

func TestCheckpointCommandRejectsCommitMessageArgumentWithConfiguredMsgSourceCmd(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, ".autosnap.toml"), []byte("check = \"true\"\nmsg_source_cmd = \"printf sourced\"\n"), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	cfg, _, err := resolveStartConfig(repo, newCheckpointCommand(), "", "", defaultAutosnapConfig().IdleSeconds, snapshotModeBoth, commitModeCheckpoint, watchModeRecursive, defaultPollInterval, defaultLogMaxBytes)
	if err != nil {
		t.Fatalf("resolve config failed: %v", err)
	}
	err = validateCheckpointCommitMessageSources("manual checkpoint message", cfg.MsgSourceCmd)
	if err == nil {
		t.Fatalf("expected checkpoint command to reject configured conflicting message source")
	}
	if !strings.Contains(err.Error(), "COMMIT_MSG cannot be used with --msg-source-cmd") {
		t.Fatalf("expected COMMIT_MSG conflict error, got %v", err)
	}
}

func TestCheckpointCommandAllowsCommitMessageArgumentWithClearedConfiguredMsgSourceCmd(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repo, ".autosnap.toml"), []byte("check = \"true\"\nmsg_source_cmd = \"printf sourced\"\n"), 0o644); err != nil {
			t.Fatalf("write config failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("cleared configured message source"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newCheckpointCommand())
		root.SetArgs([]string{"checkpoint", "manual checkpoint message", "--msg-source-cmd", ""})
		if err := root.Execute(); err != nil {
			t.Fatalf("checkpoint command failed: %v", err)
		}

		checkpoints, err := listCheckpointRefsForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("list checkpoints failed: %v", err)
		}
		if len(checkpoints) != 1 {
			t.Fatalf("expected one checkpoint, got %d", len(checkpoints))
		}
		if got := runGitOutput(t, repo, "log", "-1", "--pretty=%s", checkpoints[0].Commit); got != "manual checkpoint message" {
			t.Fatalf("expected checkpoint subject %q, got %q", "manual checkpoint message", got)
		}
	})
}

func TestCheckpointCommandReturnsErrorWhenCheckFails(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("failed checkpoint"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newCheckpointCommand())
		root.SetArgs([]string{"checkpoint", "--check", "false"})
		if err := root.Execute(); err == nil {
			t.Fatalf("expected checkpoint command to fail")
		}

		checkpoints, err := listCheckpointRefsForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("list checkpoints failed: %v", err)
		}
		if len(checkpoints) != 0 {
			t.Fatalf("expected no checkpoints, got %d", len(checkpoints))
		}
	})
}

func TestCheckpointCommandAllowsActiveDaemonState(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("daemon active checkpoint"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}

		runPath, err := runStatePath(repo)
		if err != nil {
			t.Fatalf("runStatePath failed: %v", err)
		}
		if err := saveAutosnapRunState(runPath, autosnapRunState{PID: os.Getpid(), RepoRoot: repo}); err != nil {
			t.Fatalf("save run state failed: %v", err)
		}

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newCheckpointCommand())
		root.SetArgs([]string{"checkpoint", "--check", "true"})
		if err := root.Execute(); err != nil {
			t.Fatalf("checkpoint command failed with active daemon state: %v", err)
		}
	})
}

func TestCheckpointCommandTimeoutWhenLockHeld(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		lock, err := acquireCheckpointLock(context.Background(), repo, 0)
		if err != nil {
			t.Fatalf("acquire checkpoint lock failed: %v", err)
		}
		defer lock.Close()

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("locked checkpoint"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newCheckpointCommand())
		root.SetArgs([]string{"checkpoint", "--check", "true", "--timeout", "1ms"})
		err = root.Execute()
		if err == nil {
			t.Fatalf("expected checkpoint command to time out waiting for lock")
		}
		if !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("expected timeout error, got %v", err)
		}
	})
}

func TestCreateCheckpointUsesCustomMessage(t *testing.T) {
	t.Parallel()
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

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("custom message checkpoint\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint failed: %v", err)
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
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("generated message checkpoint\n"), 0o644); err != nil {
			t.Fatalf("write checkpoint failed: %v", err)
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
