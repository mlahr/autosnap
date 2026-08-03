package autosnap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func requireIntegration(t *testing.T) {
	if !runIntegrationTests {
		t.Skip("run integration test with: go test -tags=integration ./...")
	}
}

func emptyStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

var testRepoTemplate struct {
	once sync.Once
	dir  string
	err  error
}

func createTestRepo(t *testing.T) string {
	t.Helper()
	testRepoTemplate.once.Do(func() {
		testRepoTemplate.dir, testRepoTemplate.err = createTestRepoTemplate()
	})
	if testRepoTemplate.err != nil {
		t.Fatalf("create test repo template failed: %v", testRepoTemplate.err)
	}

	dir := t.TempDir()
	if err := copyDir(testRepoTemplate.dir, dir); err != nil {
		t.Fatalf("copy test repo template failed: %v", err)
	}
	return dir
}

func createTestRepoTemplate() (string, error) {
	dir, err := os.MkdirTemp("", "autosnap-test-repo-template-")
	if err != nil {
		return "", err
	}
	sub := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		return "", err
	}
	cmds := [][]string{
		{"init", "-q"},
		{"config", "user.name", "Autosnap Test"},
		{"config", "user.email", "test@autosnap.local"},
	}
	for _, args := range cmds {
		if err := runGitTemplate(dir, args...); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("initial"), 0o644); err != nil {
		return "", err
	}
	if err := runGitTemplate(dir, "add", "file.txt"); err != nil {
		return "", err
	}
	if err := runGitTemplate(dir, "commit", "-m", "initial commit"); err != nil {
		return "", err
	}
	return dir, nil
}

func runGitTemplate(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %w: %s", strings.Join(args, " "), err, out)
	}
	return nil
}

func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, info.Mode().Perm()); err != nil {
				return err
			}
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v: %s", strings.Join(args, " "), err, out)
	}
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s failed: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

var testWorkingDirs sync.Map

func init() {
	detectRepositoryStartDir = func() string {
		if dir, ok := testWorkingDirs.Load(currentGoroutineID()); ok {
			return dir.(string)
		}
		return "."
	}
}

func withWorkingDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	gid := currentGoroutineID()
	testWorkingDirs.Store(gid, dir)
	defer testWorkingDirs.Delete(gid)
	fn()
}

func currentGoroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	fields := strings.Fields(string(buf[:n]))
	id, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		panic(fmt.Sprintf("parse goroutine id: %v", err))
	}
	return id
}

func createPickConflictScenario(t *testing.T, repo string) string {
	t.Helper()
	originalNow := currentTimestampFn
	timestamps := []string{"20260101T120000Z", "20260101T120001Z"}
	i := 0
	currentTimestampFn = func() string {
		value := timestamps[i]
		i++
		return value
	}
	t.Cleanup(func() { currentTimestampFn = originalNow })

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

	return ref2
}

func createCheckpointRangeScenario(t *testing.T, repo string) (string, string, string) {
	t.Helper()
	originalNow := currentTimestampFn
	timestamps := []string{"20260101T120000Z", "20260101T120001Z", "20260101T120002Z"}
	i := 0
	currentTimestampFn = func() string {
		value := timestamps[i]
		i++
		return value
	}
	t.Cleanup(func() { currentTimestampFn = originalNow })

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
		t.Fatalf("write second checkpoint file failed: %v", err)
	}
	tree2, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
	if err != nil {
		t.Fatalf("computeWorktreeTree second failed: %v", err)
	}
	ref2, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree2, "second")
	if err != nil {
		t.Fatalf("create second checkpoint failed: %v", err)
	}

	if err := os.Remove(filepath.Join(repo, "bad.txt")); err != nil {
		t.Fatalf("remove bad checkpoint file failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "good.txt"), []byte("good checkpoint 3\n"), 0o644); err != nil {
		t.Fatalf("write third checkpoint file failed: %v", err)
	}
	tree3, err := computeWorktreeTree(context.Background(), repo, gitDirectory, snapshotModeBoth)
	if err != nil {
		t.Fatalf("computeWorktreeTree third failed: %v", err)
	}
	ref3, _, err := createCheckpoint(context.Background(), repo, branchRef, "npm test", 5*time.Second, tree3, "third")
	if err != nil {
		t.Fatalf("create third checkpoint failed: %v", err)
	}

	return ref1, ref2, ref3
}

func testTreeFileContent(t *testing.T, repoRoot, tree, filePath string) string {
	t.Helper()
	content := runGitOutput(t, repoRoot, "show", tree+":"+filePath)
	return strings.TrimSuffix(content, "\n")
}

func createAutosnapTestRef(t *testing.T, repoRoot, branchRef, timestamp string) string {
	t.Helper()
	ref := snapshotRef(branchRef, timestamp)
	commit := runGitOutput(t, repoRoot, "rev-parse", "HEAD")
	runGit(t, repoRoot, "update-ref", ref, commit)
	return ref
}

func createAutosnapTestCommitRef(t *testing.T, repoRoot, branchRef, timestamp, message string) string {
	t.Helper()
	tree := runGitOutput(t, repoRoot, "rev-parse", "HEAD^{tree}")
	parent := runGitOutput(t, repoRoot, "rev-parse", "HEAD")
	result, err := runGitCommandWithInput(context.Background(), repoRoot, nil, message, "commit-tree", tree, "-p", parent, "-F", "-")
	if err != nil {
		t.Fatalf("create test commit failed: %v", err)
	}
	commit := strings.TrimSpace(result.Stdout)
	ref := snapshotRef(branchRef, timestamp)
	runGit(t, repoRoot, "update-ref", ref, commit)
	return ref
}

func createAutosnapTestCommitRefFromIndex(t *testing.T, repoRoot, branchRef, timestamp, message string) string {
	t.Helper()
	tree := runGitOutput(t, repoRoot, "write-tree")
	return createAutosnapTestCommitRefFromTree(t, repoRoot, branchRef, timestamp, tree, message)
}

func createAutosnapTestCommitRefFromTree(t *testing.T, repoRoot, branchRef, timestamp, tree, message string) string {
	t.Helper()
	parent := runGitOutput(t, repoRoot, "rev-parse", "HEAD")
	result, err := runGitCommandWithInput(context.Background(), repoRoot, nil, message, "commit-tree", tree, "-p", parent, "-F", "-")
	if err != nil {
		t.Fatalf("create test commit failed: %v", err)
	}
	commit := strings.TrimSpace(result.Stdout)
	ref := snapshotRef(branchRef, timestamp)
	runGit(t, repoRoot, "update-ref", ref, commit)
	return ref
}

func addGitNote(t *testing.T, repoRoot, noteRef, object, note string) {
	t.Helper()
	if _, err := runGitCommandWithInput(context.Background(), repoRoot, nil, note, "notes", "--ref", noteRef, "add", "-f", "-F", "-", object); err != nil {
		t.Fatalf("add git note failed: %v", err)
	}
}

func parseJSONLRows(t *testing.T, output string) []map[string]any {
	t.Helper()
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil
	}

	lines := strings.Split(trimmed, "\n")
	rows := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatalf("parse JSONL line %q failed: %v", line, err)
		}
		rows = append(rows, row)
	}
	return rows
}

func parseJSONRows(t *testing.T, output string) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal([]byte(output), &rows); err != nil {
		t.Fatalf("parse JSON output %q failed: %v", output, err)
	}
	return rows
}

func lineContaining(output, needle string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

func stringPointer(value string) *string {
	return &value
}

func writeTestFiles(t *testing.T, repoRoot string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		fullPath := filepath.Join(repoRoot, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("create test file directory failed: %v", err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			t.Fatalf("write test file failed: %v", err)
		}
	}
}

func jsonRowsByRef(rows []map[string]any) map[string]map[string]any {
	byRef := map[string]map[string]any{}
	for _, row := range rows {
		ref, _ := row["ref"].(string)
		byRef[ref] = row
	}
	return byRef
}

func gitRefExists(t *testing.T, repoRoot, ref string) bool {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--verify", ref)
	cmd.Dir = repoRoot
	return cmd.Run() == nil
}
