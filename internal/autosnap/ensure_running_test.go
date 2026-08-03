package autosnap

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEnsureRunningRequiresRepositoryConfig(t *testing.T) {
	repo := createTestRepo(t)
	withWorkingDir(t, repo, func() {
		err := ensureAutosnapRunning(context.Background(), repo, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), ".autosnap.toml does not exist") {
			t.Fatalf("expected missing config error, got %v", err)
		}
	})
}

func TestAwaitStartedDaemonTerminatesAndReapsUnreadyProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test helper uses a POSIX shell signal trap")
	}
	repo := createTestRepo(t)
	cmd := exec.Command("sh", "-c", `trap 'exit 0' INT; while :; do sleep 0.1; done`)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start unready process failed: %v", err)
	}

	err := awaitStartedDaemon(context.Background(), repo, "unready-token", cmd.Process, 25*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not become ready") {
		t.Fatalf("expected readiness error, got %v", err)
	}
	if signalErr := cmd.Process.Signal(os.Signal(nil)); signalErr == nil {
		t.Fatalf("expected timed-out process %d to be reaped", cmd.Process.Pid)
	}
	active, activeErr := activeAutosnapRun(repo)
	if activeErr != nil {
		t.Fatalf("read daemon state failed: %v", activeErr)
	}
	if active {
		t.Fatalf("expected no active daemon after readiness cleanup")
	}
}

func TestEnsureRunningStartsExactlyOneDaemonConcurrently(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	if err := os.WriteFile(autosnapConfigPath(repo), []byte("check = \"true\"\nidle_seconds = 60\n[watch]\nmode = \"poll\"\npoll_interval = \"1s\"\n"), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path failed")
	}
	projectRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", ".."))
	binary := filepath.Join(t.TempDir(), "autosnap")
	build := exec.Command("go", "build", "-o", binary, "./cmd/autosnap")
	build.Dir = projectRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build autosnap failed: %v: %s", err, output)
	}
	t.Cleanup(func() {
		cmd := exec.Command(binary, "stop")
		cmd.Dir = repo
		_, _ = cmd.CombinedOutput()
	})

	type result struct {
		output []byte
		err    error
	}
	results := make([]result, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			cmd := exec.Command(binary, "ensure-running")
			cmd.Dir = repo
			results[index].output, results[index].err = cmd.CombinedOutput()
		}(i)
	}
	wg.Wait()

	started := 0
	for i, result := range results {
		if result.err != nil {
			t.Fatalf("ensure-running %d failed: %v: %s", i, result.err, result.output)
		}
		if bytes.Contains(result.output, []byte("autosnap ensured running")) {
			started++
		}
	}
	if started != 1 {
		t.Fatalf("expected exactly one caller to start the daemon, got %d outputs: %q / %q", started, results[0].output, results[1].output)
	}

	runPath, err := runStatePath(repo)
	if err != nil {
		t.Fatalf("resolve run state failed: %v", err)
	}
	state, err := loadAutosnapRunState(runPath)
	if err != nil || !isAutosnapRunActive(state) {
		t.Fatalf("expected one active daemon, state=%+v err=%v", state, err)
	}

	cmd := exec.Command(binary, "ensure-running")
	cmd.Dir = repo
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("already-running ensure failed: %v: %s", err, output)
	}
	if len(output) != 0 {
		t.Fatalf("expected already-running ensure to be silent, got %q", output)
	}
}
