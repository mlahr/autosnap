package autosnap

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
)

func newStopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the autosnap daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			repoRoot, _, _, err := detectRepository(ctx)
			if err != nil {
				return err
			}

			_, err = stopAutosnapRun(repoRoot, cmd.OutOrStdout())
			return err
		},
	}
}

func stopAutosnapRun(repoRoot string, out io.Writer) (autosnapRunState, error) {
	runPath, err := runStatePath(repoRoot)
	if err != nil {
		return autosnapRunState{}, err
	}

	state, err := loadAutosnapRunState(runPath)
	if err != nil {
		return autosnapRunState{}, err
	}
	if state.PID == 0 || !isAutosnapRunActive(state) {
		fmt.Fprintf(out, "no running autosnap process found (stale pid=%d)\n", state.PID)
		return state, removeAutosnapRunState(runPath)
	}

	process, err := os.FindProcess(state.PID)
	if err != nil {
		return state, removeAutosnapRunState(runPath)
	}

	if err := process.Signal(os.Interrupt); err != nil {
		fmt.Fprintf(out, "failed graceful stop for pid=%d, forcing: %v\n", state.PID, err)
	}

	if waitForPidExit(state.PID, 2*time.Second) {
		if err := removeAutosnapRunState(runPath); err != nil {
			return state, err
		}
		fmt.Fprintln(out, "autosnap stopped")
		return state, nil
	}

	if err := process.Signal(os.Kill); err != nil {
		if isProcessAlive(state.PID) {
			return state, err
		}
		if err := removeAutosnapRunState(runPath); err != nil {
			return state, err
		}
		fmt.Fprintln(out, "autosnap stopped")
		return state, nil
	}
	if waitForPidExit(state.PID, 2*time.Second) {
		if err := removeAutosnapRunState(runPath); err != nil {
			return state, err
		}
		fmt.Fprintln(out, "autosnap stopped")
		return state, nil
	}

	return state, fmt.Errorf("autosnap process %d did not exit", state.PID)
}

func waitForPidExit(pid int, timeout time.Duration) bool {
	deadline := time.After(timeout)
	ticker := time.NewTicker(75 * time.Millisecond)
	defer ticker.Stop()

	for {
		if !isProcessAlive(pid) {
			return true
		}

		select {
		case <-deadline:
			return false
		case <-ticker.C:
		}
	}
}
