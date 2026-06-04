package autosnap

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

func newStopCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop a background autosnap watcher",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			repoRoot, _, _, err := detectRepository(ctx)
			if err != nil {
				return err
			}

			runPath, err := runStatePath(repoRoot)
			if err != nil {
				return err
			}

			state, err := loadAutosnapRunState(runPath)
			if err != nil {
				return err
			}
			if state.PID == 0 || !isProcessAlive(state.PID) {
				fmt.Printf("no running autosnap process found (stale pid=%d)\n", state.PID)
				return removeAutosnapRunState(runPath)
			}

			process, err := os.FindProcess(state.PID)
			if err != nil {
				return removeAutosnapRunState(runPath)
			}

			if err := process.Signal(os.Interrupt); err != nil {
				fmt.Printf("failed graceful stop for pid=%d, forcing: %v\n", state.PID, err)
			}

			if waitForPidExit(state.PID, 2*time.Second) {
				if err := removeAutosnapRunState(runPath); err != nil {
					return err
				}
				fmt.Println("autosnap stopped")
				return nil
			}

			if err := process.Signal(os.Kill); err != nil {
				if isProcessAlive(state.PID) {
					return err
				}
				if err := removeAutosnapRunState(runPath); err != nil {
					return err
				}
				fmt.Println("autosnap stopped")
				return nil
			}
			if waitForPidExit(state.PID, 2*time.Second) {
				if err := removeAutosnapRunState(runPath); err != nil {
					return err
				}
				fmt.Println("autosnap stopped")
				return nil
			}

			return fmt.Errorf("autosnap process %d did not exit", state.PID)
		},
	}
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
