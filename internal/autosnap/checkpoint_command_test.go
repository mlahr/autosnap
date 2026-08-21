package autosnap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

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

func TestRunCheckAppendsMessageBodySourceToGeneratedMessage(t *testing.T) {
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
		bodySourceCmd := "printf 'body-base:%s\\nbody-prev:%s\\nbody-branch:%s\\nbody-head:%s\\n' \"$AUTOSNAP_DIFF_BASE\" \"$AUTOSNAP_PREVIOUS_CHECKPOINT_REF\" \"$AUTOSNAP_BRANCH_REF\" \"$AUTOSNAP_HEAD\""
		runner, err := newSnapshotRunnerWithWatchAndBody(ctx, repoRoot, branchRef, "true", "", bodySourceCmd, snapshotModeBoth, commitModeCheckpoint, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunnerWithWatchAndBody failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repoRoot, "body.txt"), []byte("body"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}

		runner.runCheck()
		message, err := getCommitMessage(ctx, repoRoot, runner.state.LastCheckpointRef)
		if err != nil {
			t.Fatalf("getCommitMessage failed: %v", err)
		}
		trimmed := strings.TrimSpace(message)
		if !strings.HasPrefix(trimmed, "autosnap: passing checkpoint") {
			t.Fatalf("expected generated message subject, got %q", trimmed)
		}
		wantBody := "body-base:" + head + "\nbody-prev:\nbody-branch:" + branchRef + "\nbody-head:" + head
		if !strings.HasSuffix(trimmed, "\n\n"+wantBody) {
			t.Fatalf("expected body after generated subject, got %q", trimmed)
		}
	})
}

func TestRunCheckAppendsMessageBodySourceToFullMessage(t *testing.T) {
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
		runner, err := newSnapshotRunnerWithWatchAndBody(ctx, repoRoot, branchRef, "true", "printf 'subject\\n\\nsource body'", "printf 'appended body'", snapshotModeBoth, commitModeCheckpoint, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunnerWithWatchAndBody failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repoRoot, "body.txt"), []byte("body"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}

		runner.runCheck()
		message, err := getCommitMessage(ctx, repoRoot, runner.state.LastCheckpointRef)
		if err != nil {
			t.Fatalf("getCommitMessage failed: %v", err)
		}
		if got, want := strings.TrimSpace(message), "subject\n\nsource body\n\nappended body"; got != want {
			t.Fatalf("expected combined commit message %q, got %q", want, got)
		}
	})
}

func TestRunCheckKeepsCheckpointWhenMessageBodySourceFails(t *testing.T) {
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
		runner, err := newSnapshotRunnerWithWatchAndBody(ctx, repoRoot, branchRef, "true", "", "printf ran > body-source-ran && exit 7", snapshotModeBoth, commitModeCheckpoint, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunnerWithWatchAndBody failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repoRoot, "body.txt"), []byte("body"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}

		runner.runCheck()
		if runner.state.LastCheckpointRef == "" {
			t.Fatalf("expected checkpoint after body source failure")
		}
		if _, err := os.Stat(filepath.Join(repoRoot, "body-source-ran")); err != nil {
			t.Fatalf("expected body source to run: %v", err)
		}
		message, err := getCommitMessage(ctx, repoRoot, runner.state.LastCheckpointRef)
		if err != nil {
			t.Fatalf("getCommitMessage failed: %v", err)
		}
		if !strings.HasPrefix(strings.TrimSpace(message), "autosnap: passing checkpoint") {
			t.Fatalf("expected generated message after body source failure, got %q", message)
		}
	})
}

func TestRunCheckDirectCommitUsesMessageBodySource(t *testing.T) {
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
		runner, err := newSnapshotRunnerWithWatchAndBody(ctx, repoRoot, branchRef, "true", "printf 'direct subject'", "printf 'direct body'", snapshotModeBoth, commitModeDirect, watchModePoll, time.Second, time.Second, statePath)
		if err != nil {
			t.Fatalf("newSnapshotRunnerWithWatchAndBody failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repoRoot, "body.txt"), []byte("body"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}

		runner.runCheck()
		if runner.state.LastCheckpointRef == "" {
			t.Fatalf("expected direct commit in state")
		}
		if got, want := strings.TrimSpace(runGitOutput(t, repoRoot, "log", "-1", "--pretty=%B")), "direct subject\n\ndirect body"; got != want {
			t.Fatalf("expected direct commit message %q, got %q", want, got)
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

func TestRunCheckExecutesPostCheckpointCommandAfterNoteAttachment(t *testing.T) {
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
		runner.noteCommand = "touch note-attached && printf note"
		runner.noteRef = "refs/notes/diffcog"
		runner.postCheckpointCommand = `test -f note-attached && printf 'commit:%s\nref:%s\nbranch:%s\n' "$AUTOSNAP_CHECKPOINT_COMMIT" "$AUTOSNAP_CHECKPOINT_REF" "$AUTOSNAP_BRANCH_REF" > post-checkpoint.txt`

		if err := os.WriteFile(filepath.Join(repoRoot, "first.txt"), []byte("first"), 0o644); err != nil {
			t.Fatalf("write first file failed: %v", err)
		}
		runner.runCheck()

		output, err := os.ReadFile(filepath.Join(repoRoot, "post-checkpoint.txt"))
		if err != nil {
			t.Fatalf("read post-checkpoint output failed: %v", err)
		}
		commit := runGitOutput(t, repoRoot, "rev-parse", runner.state.LastCheckpointRef)
		for _, want := range []string{
			"commit:" + commit,
			"ref:" + runner.state.LastCheckpointRef,
			"branch:" + branchRef,
		} {
			if !strings.Contains(string(output), want) {
				t.Fatalf("expected post-checkpoint output to contain %q, got %q", want, output)
			}
		}
	})
}

func TestCheckpointCommandPostCheckpointFailureKeepsCheckpoint(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("post failure"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newCheckpointCommand())
		root.SetArgs([]string{"checkpoint", "--check", "true", "--post-checkpoint-command", "false"})
		if err := root.Execute(); err != nil {
			t.Fatalf("expected post-checkpoint failure to be non-fatal, got %v", err)
		}

		checkpoints, err := listCheckpointRefsForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("list checkpoints failed: %v", err)
		}
		if len(checkpoints) != 1 {
			t.Fatalf("expected one checkpoint despite post-checkpoint failure, got %d", len(checkpoints))
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

func TestCheckpointCommandSkipsMessageBodySourceForCommitMessageArgument(t *testing.T) {
	t.Parallel()
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, branchRef, err := detectRepository(context.Background())
		if err != nil {
			t.Fatalf("detectRepository failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repo, ".autosnap.toml"), []byte("check = \"true\"\nmsg_body_source_cmd = \"printf ran > body-source-ran && printf body\"\n"), 0o644); err != nil {
			t.Fatalf("write config failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("manual message checkpoint"), 0o644); err != nil {
			t.Fatalf("write file failed: %v", err)
		}

		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newCheckpointCommand())
		root.SetArgs([]string{"checkpoint", "manual checkpoint message"})
		if err := root.Execute(); err != nil {
			t.Fatalf("checkpoint command failed: %v", err)
		}

		if _, err := os.Stat(filepath.Join(repo, "body-source-ran")); !os.IsNotExist(err) {
			t.Fatalf("expected message body source not to run, stat err=%v", err)
		}
		checkpoints, err := listCheckpointRefsForBranch(context.Background(), repo, branchRef)
		if err != nil {
			t.Fatalf("list checkpoints failed: %v", err)
		}
		if len(checkpoints) != 1 {
			t.Fatalf("expected one checkpoint, got %d", len(checkpoints))
		}
		if got := strings.TrimSpace(runGitOutput(t, repo, "log", "-1", "--pretty=%B", checkpoints[0].Commit)); got != "manual checkpoint message" {
			t.Fatalf("expected complete commit message, got %q", got)
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

func TestAutosnapCommitMessageAppendsBody(t *testing.T) {
	position := gitPosition{BranchRef: "main", Head: "abcdef1234567890"}
	got := autosnapCommitMessage("subject\n\nsource body", "\n body output \n", "20260101T120000Z", position, "true", 5*time.Second)
	if want := "subject\n\nsource body\n\nbody output"; got != want {
		t.Fatalf("expected appended commit body %q, got %q", want, got)
	}
}

func TestAutosnapCommitMessageAppendsBodyToGeneratedMessage(t *testing.T) {
	position := gitPosition{BranchRef: "main", Head: "abcdef1234567890"}
	got := autosnapCommitMessage("", "body output", "20260101T120000Z", position, "true", 5*time.Second)
	want := "autosnap: passing checkpoint 20260101T120000Z branch: main check: true idle_seconds: 5 base: abcdef1\n\nbody output"
	if got != want {
		t.Fatalf("expected generated message with body %q, got %q", want, got)
	}
}
