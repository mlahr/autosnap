package autosnap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fsnotify/fsnotify"
)

func TestWatchDirectoryTreeBatchesDirectoryIgnoreChecks(t *testing.T) {
	repo := t.TempDir()
	for _, dir := range []string{
		"ignored/child",
		"watched/nested",
		"with space",
		"build/generated",
		"autosnap-ignored/child",
	} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0o755); err != nil {
			t.Fatalf("create directory %q: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "watched", "file.txt"), []byte("content"), 0o644); err != nil {
		t.Fatalf("write watched file: %v", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("create watcher: %v", err)
	}
	defer watcher.Close()

	var calls [][]string
	runner := &snapshotRunner{
		repoRoot:         repo,
		watcher:          watcher,
		ignoreCache:      map[string]bool{"": false},
		watchIgnoreRules: []ignoreRule{{pattern: "autosnap-ignored", directory: true}},
		gitIgnoredPathsFn: func(paths []string) (map[string]bool, error) {
			calls = append(calls, append([]string(nil), paths...))
			return map[string]bool{"ignored": true}, nil
		},
	}

	if err := runner.watchDirectoryTree(repo); err != nil {
		t.Fatalf("register directory tree: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("git ignore batch calls=%d, want 1", len(calls))
	}
	for _, path := range calls[0] {
		if path == "watched/file.txt" {
			t.Fatal("file was included in directory ignore batch")
		}
		if path == "build" || path == "build/generated" || path == "autosnap-ignored" || path == "autosnap-ignored/child" {
			t.Fatalf("locally ignored directory %q was included in Git batch", path)
		}
	}
	for _, want := range []string{"ignored", "ignored/child", "watched", "watched/nested", "with space"} {
		if !slices.Contains(calls[0], want) {
			t.Fatalf("Git batch missing %q: %q", want, calls[0])
		}
	}

	watched := watcher.WatchList()
	for _, want := range []string{repo, filepath.Join(repo, "watched"), filepath.Join(repo, "watched", "nested"), filepath.Join(repo, "with space")} {
		if !slices.Contains(watched, want) {
			t.Fatalf("watch list missing %q: %q", want, watched)
		}
	}
	for _, unwanted := range []string{filepath.Join(repo, "ignored"), filepath.Join(repo, "ignored", "child"), filepath.Join(repo, "build"), filepath.Join(repo, "autosnap-ignored")} {
		if slices.Contains(watched, unwanted) {
			t.Fatalf("ignored directory %q was watched: %q", unwanted, watched)
		}
	}
}

func TestWatchDirectoryTreeGitIgnoreFailureIsFailOpen(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, "candidate")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("create candidate directory: %v", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("create watcher: %v", err)
	}
	defer watcher.Close()

	runner := &snapshotRunner{
		repoRoot:    repo,
		watcher:     watcher,
		ignoreCache: map[string]bool{"": false},
		gitIgnoredPathsFn: func([]string) (map[string]bool, error) {
			return nil, errors.New("Git unavailable")
		},
	}
	if err := runner.watchDirectoryTree(repo); err != nil {
		t.Fatalf("register directory tree: %v", err)
	}
	if !slices.Contains(watcher.WatchList(), dir) {
		t.Fatalf("candidate directory was not watched after Git failure: %q", watcher.WatchList())
	}
	if _, cached := runner.getIgnoredFromCache("candidate"); cached {
		t.Fatal("failed Git result was cached")
	}
}

func TestWatchDirectoryTreeBatchesCreatedSubtree(t *testing.T) {
	repo := t.TempDir()
	created := filepath.Join(repo, "created")
	if err := os.MkdirAll(filepath.Join(created, "nested"), 0o755); err != nil {
		t.Fatalf("create subtree: %v", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatalf("create watcher: %v", err)
	}
	defer watcher.Close()

	var calls [][]string
	runner := &snapshotRunner{
		repoRoot:    repo,
		watcher:     watcher,
		ignoreCache: map[string]bool{"": false},
		gitIgnoredPathsFn: func(paths []string) (map[string]bool, error) {
			calls = append(calls, append([]string(nil), paths...))
			return map[string]bool{}, nil
		},
	}
	if err := runner.watchDirectoryTree(created); err != nil {
		t.Fatalf("register created subtree: %v", err)
	}
	if len(calls) != 1 || !slices.Equal(calls[0], []string{"created", "created/nested"}) {
		t.Fatalf("unexpected Git batches: %q", calls)
	}
	for _, want := range []string{created, filepath.Join(created, "nested")} {
		if !slices.Contains(watcher.WatchList(), want) {
			t.Fatalf("watch list missing %q: %q", want, watcher.WatchList())
		}
	}
}

func TestGitIgnoredPathsHandlesNULTerminatedPaths(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	ignoredDir := "ignored directory"
	ignoredWithNewline := "ignored\nnewline"
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(ignoredDir+"/\nignored?newline/\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	for _, dir := range []string{ignoredDir, ignoredWithNewline} {
		if err := os.Mkdir(filepath.Join(repo, dir), 0o755); err != nil {
			t.Fatalf("create ignored directory %q: %v", dir, err)
		}
	}

	ignored, err := gitIgnoredPaths(context.Background(), repo, []string{"watched", ignoredDir, ignoredWithNewline})
	if err != nil {
		t.Fatalf("batch Git ignore check: %v", err)
	}
	if ignored["watched"] || !ignored[ignoredDir] || !ignored[ignoredWithNewline] {
		t.Fatalf("unexpected ignored paths: %#v", ignored)
	}
}

func TestGitIgnoredPathsHandlesSubmoduleDescendants(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored/\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	for _, dir := range []string{"ignored", "submodule/child"} {
		if err := os.MkdirAll(filepath.Join(repo, dir), 0o755); err != nil {
			t.Fatalf("create directory %q: %v", dir, err)
		}
	}
	head := strings.TrimSpace(runGitOutput(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+head+",submodule")

	ignored, err := gitIgnoredPaths(context.Background(), repo, []string{"ignored", "submodule", "submodule/child"})
	if err != nil {
		t.Fatalf("batch Git ignore check with submodule descendant: %v", err)
	}
	if !ignored["ignored"] || ignored["submodule"] || ignored["submodule/child"] {
		t.Fatalf("unexpected ignored paths: %#v", ignored)
	}
}
