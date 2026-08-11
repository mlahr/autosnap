package autosnap

import (
	"context"
	"testing"
	"time"
)

func TestStartRecursiveSignalsReadyAfterSlowWatcherRegistration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	registered := make(chan struct{})
	ready := make(chan struct{})
	runner := &snapshotRunner{
		ctx:      ctx,
		repoRoot: t.TempDir(),
		idle:     time.Hour,
		watchDirectoryTreeFn: func(string) error {
			time.Sleep(50 * time.Millisecond)
			close(registered)
			return nil
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- runner.startRecursive(func() error {
			close(ready)
			return nil
		})
	}()

	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("recursive watcher did not signal readiness")
	}
	select {
	case <-registered:
	default:
		t.Fatal("recursive watcher signaled readiness before registration completed")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("recursive watcher exited with error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("recursive watcher did not stop")
	}
}
