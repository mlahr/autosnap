package autosnap

import (
	"bytes"
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

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

func TestPromoteCommandResolvesLastSelector(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		_, _, latestRef := createCheckpointRangeScenario(t, repo)

		runGit(t, repo, "reset", "--hard", "HEAD")
		runGit(t, repo, "clean", "-fd")

		buf := &bytes.Buffer{}
		root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
		root.AddCommand(newPromoteCommand())
		root.SetOut(buf)
		root.SetErr(buf)
		root.SetArgs([]string{"promote", "last"})

		if err := root.Execute(); err != nil {
			t.Fatalf("promote last failed: %v", err)
		}
		if !strings.Contains(buf.String(), "checkpoint promoted: "+path.Base(latestRef)) {
			t.Fatalf("expected promote output for latest checkpoint %s, got %q", latestRef, buf.String())
		}

		wantTree := runGitOutput(t, repo, "rev-parse", latestRef+"^{tree}")
		if gotTree := runGitOutput(t, repo, "rev-parse", "HEAD^{tree}"); gotTree != wantTree {
			t.Fatalf("expected promoted HEAD tree %s, got %s", wantTree, gotTree)
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
