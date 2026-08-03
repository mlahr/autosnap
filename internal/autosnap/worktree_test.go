package autosnap

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

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
