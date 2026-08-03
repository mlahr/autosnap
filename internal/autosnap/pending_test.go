package autosnap

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

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
