package autosnap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

const (
	daemonStartLockTimeout    = 5 * time.Second
	defaultDaemonReadyTimeout = 30 * time.Second
)

func newEnsureRunningCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ensure-running",
		Short: "Start the autosnap daemon unless it is already running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, _, _, err := detectRepository(context.Background())
			if err != nil {
				return err
			}
			return ensureAutosnapRunning(context.Background(), repoRoot, cmd.OutOrStdout())
		},
	}
}

func daemonStartLockPath(repoRoot string) (string, error) {
	statePath, err := stateFilePath(repoRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(statePath), "daemon-start.lock"), nil
}

func ensureAutosnapRunning(ctx context.Context, repoRoot string, out io.Writer) error {
	lockPath, err := daemonStartLockPath(repoRoot)
	if err != nil {
		return err
	}
	lock, err := acquireFileLock(ctx, lockPath, daemonStartLockTimeout, "daemon start")
	if err != nil {
		return err
	}
	defer lock.Close()

	active, err := activeAutosnapRun(repoRoot)
	if err != nil {
		return err
	}
	if active {
		return nil
	}

	configPath := autosnapConfigPath(repoRoot)
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s does not exist; run 'autosnap config init'", configPath)
		}
		return err
	}
	cfg, found, err := resolveAutosnapConfig(repoRoot, autosnapConfigOverrides{set: map[string]bool{}}, true)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%s does not exist; run 'autosnap config init'", configPath)
	}

	runToken, err := newRunToken()
	if err != nil {
		return err
	}
	process, err := startAutosnapDetached(repoRoot, cfg.Check, cfg.MsgSourceCmd, cfg.MsgBodySourceCmd, cfg.NoteCommand, cfg.NoteRef, cfg.PostCheckpointCommand, cfg.IdleSeconds, cfg.SnapshotMode, cfg.CommitMode, cfg.Watch.Mode, cfg.Watch.PollInterval, cfg.LogMaxBytes, runToken, nil)
	if err != nil {
		return err
	}
	if err := awaitStartedDaemon(ctx, repoRoot, runToken, process, cfg.ReadyTimeout); err != nil {
		return err
	}
	fmt.Fprintf(out, "autosnap ensured running (pid=%d)\n", process.Pid)
	return nil
}

func awaitStartedDaemon(ctx context.Context, repoRoot, runToken string, process *os.Process, timeout time.Duration) error {
	if err := waitForAutosnapRun(ctx, repoRoot, runToken, timeout); err != nil {
		cleanupErr := terminateUnreadyDaemon(repoRoot, runToken, process)
		if cleanupErr != nil {
			return fmt.Errorf("daemon pid=%d did not become ready: %w; cleanup failed: %v", process.Pid, err, cleanupErr)
		}
		return fmt.Errorf("daemon pid=%d did not become ready: %w", process.Pid, err)
	}
	if err := process.Release(); err != nil {
		return fmt.Errorf("release ready daemon pid=%d: %w", process.Pid, err)
	}
	return nil
}

func terminateUnreadyDaemon(repoRoot, runToken string, process *os.Process) error {
	waitCh := make(chan error, 1)
	if err := process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		_ = process.Kill()
	}
	go func() {
		_, err := process.Wait()
		waitCh <- err
	}()

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case waitErr := <-waitCh:
		if err := removeRunStateForToken(repoRoot, runToken); err != nil {
			return err
		}
		if waitErr != nil {
			if _, ok := waitErr.(*os.SyscallError); !ok {
				return waitErr
			}
		}
		return nil
	case <-timer.C:
	}

	if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-waitCh:
	case <-time.After(2 * time.Second):
		return fmt.Errorf("daemon pid=%d did not exit after kill", process.Pid)
	}
	return removeRunStateForToken(repoRoot, runToken)
}

func removeRunStateForToken(repoRoot, runToken string) error {
	runPath, err := runStatePath(repoRoot)
	if err != nil {
		return err
	}
	state, err := loadAutosnapRunState(runPath)
	if err != nil {
		return err
	}
	if state.RunToken != runToken {
		return nil
	}
	return removeAutosnapRunState(runPath)
}

func activeAutosnapRun(repoRoot string) (bool, error) {
	runPath, err := runStatePath(repoRoot)
	if err != nil {
		return false, err
	}
	state, err := loadAutosnapRunState(runPath)
	if err != nil {
		return false, err
	}
	if state.PID == 0 || !isAutosnapRunActive(state) {
		if err := removeAutosnapRunState(runPath); err != nil {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

func waitForAutosnapRun(ctx context.Context, repoRoot, runToken string, timeout time.Duration) error {
	runPath, err := runStatePath(repoRoot)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for {
		state, loadErr := loadAutosnapRunState(runPath)
		if loadErr == nil && state.PID != 0 && state.RunToken == runToken && isAutosnapRunActive(state) {
			return nil
		}
		if time.Now().After(deadline) {
			if loadErr != nil {
				return loadErr
			}
			return fmt.Errorf("timed out after %s", timeout)
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
