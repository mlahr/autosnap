package autosnap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
