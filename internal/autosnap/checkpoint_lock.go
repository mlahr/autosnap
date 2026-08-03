package autosnap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

var errCheckpointLockBusy = errors.New("checkpoint lock is held")

type fileLock struct {
	file *os.File
}

func checkpointLockPath(repoRoot string) (string, error) {
	statePath, err := stateFilePath(repoRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(statePath), "checkpoint.lock"), nil
}

func acquireCheckpointLock(ctx context.Context, repoRoot string, timeout time.Duration) (*fileLock, error) {
	path, err := checkpointLockPath(repoRoot)
	if err != nil {
		return nil, err
	}
	return acquireFileLock(ctx, path, timeout, "checkpoint")
}

func acquireFileLock(ctx context.Context, path string, timeout time.Duration, label string) (*fileLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}

	deadline := time.Time{}
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}

	for {
		err := tryLockCheckpointFile(file)
		if err == nil {
			return &fileLock{file: file}, nil
		}
		if !errors.Is(err, errCheckpointLockBusy) {
			_ = file.Close()
			return nil, err
		}
		if timeout > 0 && !time.Now().Before(deadline) {
			_ = file.Close()
			return nil, fmt.Errorf("timed out after %s waiting for %s lock", timeout, label)
		}

		wait := 50 * time.Millisecond
		if timeout > 0 {
			remaining := time.Until(deadline)
			if remaining <= 0 {
				_ = file.Close()
				return nil, fmt.Errorf("timed out after %s waiting for %s lock", timeout, label)
			}
			if remaining < wait {
				wait = remaining
			}
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			_ = file.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (l *fileLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := unlockCheckpointFile(l.file)
	closeErr := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	return closeErr
}
