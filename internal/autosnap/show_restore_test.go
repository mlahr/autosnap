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
