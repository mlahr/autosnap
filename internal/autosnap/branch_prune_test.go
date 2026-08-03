package autosnap

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

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
