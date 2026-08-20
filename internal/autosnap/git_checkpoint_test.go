package autosnap

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

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

func TestDetachedBranchIdentityUsesStableShortHash(t *testing.T) {
	display, ref := detachedBranchIdentity("93465563f9feaf3be6bed58388a1c2d93ee4b941")
	if display != "detached@9346556" {
		t.Fatalf("expected detached display name detached@9346556, got %q", display)
	}
	if ref != "detached-9346556" {
		t.Fatalf("expected detached ref detached-9346556, got %q", ref)
	}
}

func TestDetachedRepositoryUsesSameBranchRefAsCurrentGitPosition(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		runGit(t, repo, "checkout", "--detach", "HEAD")

		_, branchDisplay, detectedRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		position, err := currentGitPosition(context.Background(), repo)
		if err != nil {
			t.Fatalf("currentGitPosition failed: %v", err)
		}

		if branchDisplay != "detached@"+strings.TrimPrefix(detectedRef, "detached-") {
			t.Fatalf("expected detached display %q to match ref %q", branchDisplay, detectedRef)
		}
		if detectedRef != position.BranchRef {
			t.Fatalf("expected detected branch ref %q to match checkpoint branch ref %q", detectedRef, position.BranchRef)
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
