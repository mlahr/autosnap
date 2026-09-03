package autosnap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func startResolvedTestMerge(t *testing.T, repo string) (string, string) {
	t.Helper()
	base := runGitOutput(t, repo, "rev-parse", "HEAD")
	mainBranch := runGitOutput(t, repo, "branch", "--show-current")
	runGit(t, repo, "checkout", "-b", "merge-feature")
	if err := os.WriteFile(filepath.Join(repo, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write feature file failed: %v", err)
	}
	runGit(t, repo, "add", "feature.txt")
	runGit(t, repo, "commit", "-m", "feature commit")
	mergeHead := runGitOutput(t, repo, "rev-parse", "HEAD")
	runGit(t, repo, "checkout", mainBranch)
	runGit(t, repo, "merge", "--no-ff", "--no-commit", "merge-feature")
	return base, mergeHead
}

func newMergeTestRunner(t *testing.T, repo, commitMode, checkCommand, msgSourceCommand string) *snapshotRunner {
	t.Helper()
	ctx := context.Background()
	repoRoot, _, branchRef, err := detectRepository(ctx)
	if err != nil {
		t.Fatalf("detectRepository failed: %v", err)
	}
	statePath, err := stateFilePath(repoRoot)
	if err != nil {
		t.Fatalf("stateFilePath failed: %v", err)
	}
	runner, err := newSnapshotRunnerWithWatch(ctx, repoRoot, branchRef, checkCommand, msgSourceCommand, snapshotModeBoth, commitMode, watchModePoll, time.Second, time.Second, statePath)
	if err != nil {
		t.Fatalf("newSnapshotRunnerWithWatch failed: %v", err)
	}
	return runner
}

func TestRunCheckCheckpointPreservesActiveMerge(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		base, mergeHead := startResolvedTestMerge(t, repo)
		wantMessage, err := activeMergeMessage(context.Background(), repo)
		if err != nil {
			t.Fatalf("activeMergeMessage failed: %v", err)
		}
		runner := newMergeTestRunner(t, repo, commitModeCheckpoint, "true", "")
		if _, err := runner.runCheckUnlocked(); err != nil {
			t.Fatalf("runCheckUnlocked failed: %v", err)
		}

		ref := runner.state.LastCheckpointRef
		if ref == "" {
			t.Fatal("expected a merge checkpoint ref")
		}
		commit := runGitOutput(t, repo, "rev-parse", ref)
		parents, err := getCommitParents(context.Background(), repo, commit)
		if err != nil {
			t.Fatalf("getCommitParents failed: %v", err)
		}
		if len(parents) != 2 || parents[0] != base || parents[1] != mergeHead {
			t.Fatalf("expected merge parents [%s %s], got %v", base, mergeHead, parents)
		}
		if got := runGitOutput(t, repo, "rev-parse", "HEAD"); got != base {
			t.Fatalf("expected checkpoint mode to keep HEAD %s, got %s", base, got)
		}
		if got := runGitOutput(t, repo, "rev-parse", "MERGE_HEAD"); got != mergeHead {
			t.Fatalf("expected checkpoint mode to keep MERGE_HEAD %s, got %s", mergeHead, got)
		}
		if got := runGitOutput(t, repo, "log", "-1", "--pretty=%B", commit); got != wantMessage {
			t.Fatalf("expected merge message %q, got %q", wantMessage, got)
		}
	})
}

func TestRunCheckDirectCompletesActiveMerge(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		base, mergeHead := startResolvedTestMerge(t, repo)
		runner := newMergeTestRunner(t, repo, commitModeDirect, "true", "")
		if _, err := runner.runCheckUnlocked(); err != nil {
			t.Fatalf("runCheckUnlocked failed: %v", err)
		}

		head := runGitOutput(t, repo, "rev-parse", "HEAD")
		parents, err := getCommitParents(context.Background(), repo, head)
		if err != nil {
			t.Fatalf("getCommitParents failed: %v", err)
		}
		if len(parents) != 2 || parents[0] != base || parents[1] != mergeHead {
			t.Fatalf("expected merge parents [%s %s], got %v", base, mergeHead, parents)
		}
		if runner.state.LastCheckpointRef != head {
			t.Fatalf("expected state commit %s, got %s", head, runner.state.LastCheckpointRef)
		}
		if _, err := os.Stat(filepath.Join(repo, ".git", "MERGE_HEAD")); !os.IsNotExist(err) {
			t.Fatalf("expected direct mode to clear MERGE_HEAD, stat error: %v", err)
		}
		if status := runGitOutput(t, repo, "status", "--porcelain"); status != "" {
			t.Fatalf("expected a clean worktree, got %q", status)
		}
	})
}

func TestRunCheckRejectsUnresolvedActiveMergeBeforeCheck(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		mainBranch := runGitOutput(t, repo, "branch", "--show-current")
		runGit(t, repo, "checkout", "-b", "conflict-feature")
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("feature\n"), 0o644); err != nil {
			t.Fatalf("write feature conflict failed: %v", err)
		}
		runGit(t, repo, "add", "file.txt")
		runGit(t, repo, "commit", "-m", "feature conflict")
		runGit(t, repo, "checkout", mainBranch)
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("main\n"), 0o644); err != nil {
			t.Fatalf("write main conflict failed: %v", err)
		}
		runGit(t, repo, "add", "file.txt")
		runGit(t, repo, "commit", "-m", "main conflict")
		result, mergeErr := runGitCommand(context.Background(), repo, nil, "merge", "--no-ff", "--no-commit", "conflict-feature")
		if mergeErr == nil {
			t.Fatal("expected merge conflict")
		}
		if !strings.Contains(result.Stdout+result.Stderr, "CONFLICT") {
			t.Fatalf("expected conflict output, got %q", result.Stdout+result.Stderr)
		}

		runner := newMergeTestRunner(t, repo, commitModeCheckpoint, "printf ran > check-ran", "")
		if _, err := runner.runCheckUnlocked(); err == nil || !strings.Contains(err.Error(), "unresolved paths: file.txt") {
			t.Fatalf("expected unresolved merge error, got %v", err)
		}
		if _, err := os.Stat(filepath.Join(repo, "check-ran")); !os.IsNotExist(err) {
			t.Fatalf("expected check command not to run, stat error: %v", err)
		}
		if runner.state.LastCheckpointRef != "" {
			t.Fatalf("expected no checkpoint, got %s", runner.state.LastCheckpointRef)
		}
	})
}

func TestRunCheckCreatesTreeNeutralMergeCheckpoint(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		mainBranch := runGitOutput(t, repo, "branch", "--show-current")
		base := runGitOutput(t, repo, "rev-parse", "HEAD")
		runGit(t, repo, "checkout", "-b", "empty-feature")
		runGit(t, repo, "commit", "--allow-empty", "-m", "empty feature")
		mergeHead := runGitOutput(t, repo, "rev-parse", "HEAD")
		runGit(t, repo, "checkout", mainBranch)
		runGit(t, repo, "merge", "--no-ff", "--no-commit", "empty-feature")

		runner := newMergeTestRunner(t, repo, commitModeCheckpoint, "true", "")
		if _, err := runner.runCheckUnlocked(); err != nil {
			t.Fatalf("runCheckUnlocked failed: %v", err)
		}
		parents, err := getCommitParents(context.Background(), repo, runner.state.LastCheckpointRef)
		if err != nil {
			t.Fatalf("getCommitParents failed: %v", err)
		}
		if len(parents) != 2 || parents[0] != base || parents[1] != mergeHead {
			t.Fatalf("expected tree-neutral merge parents [%s %s], got %v", base, mergeHead, parents)
		}
	})
}

func TestPromoteCheckpointPreservesMergeParents(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		base, mergeHead := startResolvedTestMerge(t, repo)
		runner := newMergeTestRunner(t, repo, commitModeCheckpoint, "true", "")
		if _, err := runner.runCheckUnlocked(); err != nil {
			t.Fatalf("runCheckUnlocked failed: %v", err)
		}
		ref := runner.state.LastCheckpointRef
		commit := runGitOutput(t, repo, "rev-parse", ref)
		promoted, created, err := promoteCheckpoint(context.Background(), repo, checkpointRefInfo{Ref: ref, Commit: commit, FullCommit: commit}, true)
		if err != nil {
			t.Fatalf("promoteCheckpoint failed: %v", err)
		}
		if !created {
			t.Fatal("expected promotion to create a merge commit")
		}
		parents, err := getCommitParents(context.Background(), repo, promoted)
		if err != nil {
			t.Fatalf("getCommitParents failed: %v", err)
		}
		if len(parents) != 2 || parents[0] != base || parents[1] != mergeHead {
			t.Fatalf("expected promoted parents [%s %s], got %v", base, mergeHead, parents)
		}
	})
}

func TestRunCheckSyncPreservesActiveMerge(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	remoteRoot := t.TempDir()
	remote := filepath.Join(remoteRoot, "origin.git")
	runGit(t, remoteRoot, "init", "--bare", remote)
	runGit(t, repo, "remote", "add", "origin", remote)
	runGit(t, repo, "push", "-u", "origin", "HEAD")

	withWorkingDir(t, repo, func() {
		base, mergeHead := startResolvedTestMerge(t, repo)
		runner := newMergeTestRunner(t, repo, commitModeSync, "true", "")
		if _, err := runner.runCheckUnlocked(); err != nil {
			t.Fatalf("runCheckUnlocked failed: %v", err)
		}
		head := runGitOutput(t, repo, "rev-parse", "HEAD")
		parents, err := getCommitParents(context.Background(), repo, head)
		if err != nil {
			t.Fatalf("getCommitParents failed: %v", err)
		}
		if len(parents) != 2 || parents[0] != base || parents[1] != mergeHead {
			t.Fatalf("expected synced merge parents [%s %s], got %v", base, mergeHead, parents)
		}
		branch := runGitOutput(t, repo, "branch", "--show-current")
		remoteHead := runGitOutput(t, repo, "ls-remote", "origin", "refs/heads/"+branch)
		if !strings.HasPrefix(remoteHead, head+"\t") {
			t.Fatalf("expected remote branch at %s, got %q", head, remoteHead)
		}
	})
}

func TestRunCheckCheckpointPreservesOctopusMergeParents(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		mainBranch := runGitOutput(t, repo, "branch", "--show-current")
		base := runGitOutput(t, repo, "rev-parse", "HEAD")

		runGit(t, repo, "checkout", "-b", "octopus-a")
		if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644); err != nil {
			t.Fatalf("write first octopus file failed: %v", err)
		}
		runGit(t, repo, "add", "a.txt")
		runGit(t, repo, "commit", "-m", "octopus a")
		parentA := runGitOutput(t, repo, "rev-parse", "HEAD")

		runGit(t, repo, "checkout", mainBranch)
		runGit(t, repo, "checkout", "-b", "octopus-b")
		if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("b\n"), 0o644); err != nil {
			t.Fatalf("write second octopus file failed: %v", err)
		}
		runGit(t, repo, "add", "b.txt")
		runGit(t, repo, "commit", "-m", "octopus b")
		parentB := runGitOutput(t, repo, "rev-parse", "HEAD")

		runGit(t, repo, "checkout", mainBranch)
		runGit(t, repo, "merge", "--no-ff", "--no-commit", "octopus-a", "octopus-b")
		runner := newMergeTestRunner(t, repo, commitModeCheckpoint, "true", "")
		if _, err := runner.runCheckUnlocked(); err != nil {
			t.Fatalf("runCheckUnlocked failed: %v", err)
		}
		parents, err := getCommitParents(context.Background(), repo, runner.state.LastCheckpointRef)
		if err != nil {
			t.Fatalf("getCommitParents failed: %v", err)
		}
		if len(parents) != 3 || parents[0] != base || parents[1] != parentA || parents[2] != parentB {
			t.Fatalf("expected octopus parents [%s %s %s], got %v", base, parentA, parentB, parents)
		}
	})
}

func TestRunCheckRejectsMergeStateChangeDuringCheck(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		startResolvedTestMerge(t, repo)
		runner := newMergeTestRunner(t, repo, commitModeCheckpoint, "git merge --abort", "")
		if _, err := runner.runCheckUnlocked(); err == nil || !strings.Contains(err.Error(), "merge state changed during check") {
			t.Fatalf("expected merge state change error, got %v", err)
		}
		if runner.state.LastCheckpointRef != "" {
			t.Fatalf("expected no checkpoint, got %s", runner.state.LastCheckpointRef)
		}
	})
}

func TestRunCheckMergeMessagePriority(t *testing.T) {
	requireIntegration(t)
	tests := []struct {
		name             string
		msgSourceCommand string
		explicitMessage  string
		wantMessage      string
	}{
		{
			name:             "message source",
			msgSourceCommand: "printf 'source merge message'",
			wantMessage:      "source merge message",
		},
		{
			name:            "explicit message",
			explicitMessage: "explicit merge message",
			wantMessage:     "explicit merge message",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := createTestRepo(t)
			withWorkingDir(t, repo, func() {
				startResolvedTestMerge(t, repo)
				runner := newMergeTestRunner(t, repo, commitModeCheckpoint, "true", tt.msgSourceCommand)
				runner.commitMsg = tt.explicitMessage
				if _, err := runner.runCheckUnlocked(); err != nil {
					t.Fatalf("runCheckUnlocked failed: %v", err)
				}
				commit := runGitOutput(t, repo, "rev-parse", runner.state.LastCheckpointRef)
				if got := runGitOutput(t, repo, "log", "-1", "--pretty=%B", commit); got != tt.wantMessage {
					t.Fatalf("expected message %q, got %q", tt.wantMessage, got)
				}
			})
		})
	}
}

func TestRunCheckMergeMessageFallbackAndBody(t *testing.T) {
	requireIntegration(t)
	t.Run("failed source falls back to merge message", func(t *testing.T) {
		repo := createTestRepo(t)
		withWorkingDir(t, repo, func() {
			startResolvedTestMerge(t, repo)
			wantMessage, err := activeMergeMessage(context.Background(), repo)
			if err != nil {
				t.Fatalf("activeMergeMessage failed: %v", err)
			}
			runner := newMergeTestRunner(t, repo, commitModeCheckpoint, "true", "false")
			if _, err := runner.runCheckUnlocked(); err != nil {
				t.Fatalf("runCheckUnlocked failed: %v", err)
			}
			commit := runGitOutput(t, repo, "rev-parse", runner.state.LastCheckpointRef)
			if got := runGitOutput(t, repo, "log", "-1", "--pretty=%B", commit); got != wantMessage {
				t.Fatalf("expected fallback message %q, got %q", wantMessage, got)
			}
		})
	})

	t.Run("body source extends merge message", func(t *testing.T) {
		repo := createTestRepo(t)
		withWorkingDir(t, repo, func() {
			startResolvedTestMerge(t, repo)
			wantMessage, err := activeMergeMessage(context.Background(), repo)
			if err != nil {
				t.Fatalf("activeMergeMessage failed: %v", err)
			}
			runner := newMergeTestRunner(t, repo, commitModeCheckpoint, "true", "")
			runner.msgBodySourceCmd = "printf 'merge body'"
			if _, err := runner.runCheckUnlocked(); err != nil {
				t.Fatalf("runCheckUnlocked failed: %v", err)
			}
			commit := runGitOutput(t, repo, "rev-parse", runner.state.LastCheckpointRef)
			if got := runGitOutput(t, repo, "log", "-1", "--pretty=%B", commit); got != wantMessage+"\n\nmerge body" {
				t.Fatalf("expected merge message with body, got %q", got)
			}
		})
	})

	t.Run("empty merge message fails", func(t *testing.T) {
		repo := createTestRepo(t)
		withWorkingDir(t, repo, func() {
			startResolvedTestMerge(t, repo)
			gitDirectory, err := gitDir(context.Background(), repo)
			if err != nil {
				t.Fatalf("gitDir failed: %v", err)
			}
			if err := os.WriteFile(filepath.Join(gitDirectory, "MERGE_MSG"), []byte(" \n"), 0o644); err != nil {
				t.Fatalf("write empty MERGE_MSG failed: %v", err)
			}
			runner := newMergeTestRunner(t, repo, commitModeCheckpoint, "true", "")
			if _, err := runner.runCheckUnlocked(); err == nil || !strings.Contains(err.Error(), "no usable MERGE_MSG text") {
				t.Fatalf("expected unusable merge message error, got %v", err)
			}
			if runner.state.LastCheckpointRef != "" {
				t.Fatalf("expected no checkpoint, got %s", runner.state.LastCheckpointRef)
			}
		})
	})
}
