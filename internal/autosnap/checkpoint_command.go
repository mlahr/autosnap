package autosnap

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func newCheckpointCommand() *cobra.Command {
	var (
		checkCommand string
		msgSourceCmd string
		snapshotMode string
		commitMode   string
		timeout      time.Duration
	)

	cmd := &cobra.Command{
		Use:   "checkpoint",
		Short: "Create a checkpoint immediately",
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout < 0 {
				return fmt.Errorf("--timeout must be greater than or equal to 0")
			}

			ctx := context.Background()
			repoRoot, _, branchRef, err := detectRepository(ctx)
			if err != nil {
				return err
			}

			cfg, _, err := resolveStartConfig(repoRoot, cmd, checkCommand, msgSourceCmd, defaultAutosnapConfig().IdleSeconds, snapshotMode, commitMode, watchModeRecursive, defaultPollInterval)
			if err != nil {
				return err
			}

			statePath, err := stateFilePath(repoRoot)
			if err != nil {
				return err
			}

			runner, err := newSnapshotRunnerWithWatch(ctx, repoRoot, branchRef, cfg.Check, cfg.MsgSourceCmd, cfg.SnapshotMode, cfg.CommitMode, cfg.Watch.Mode, cfg.Watch.PollInterval, time.Duration(cfg.IdleSeconds)*time.Second, statePath)
			if err != nil {
				return err
			}

			result, err := runner.runCheckWithLock(timeout)
			if err != nil {
				return err
			}
			if result.CheckFailed {
				return fmt.Errorf("check failed with exit code %d", result.CheckExit)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&checkCommand, "check", "", "Shell command to run before checkpointing")
	cmd.Flags().StringVar(&msgSourceCmd, "msg-source-cmd", "", "Shell command that returns the checkpoint commit message (multiline supported)")
	cmd.Flags().StringVar(&snapshotMode, "snapshot-mode", snapshotModeBoth, "Snapshot source: both, staged, working")
	cmd.Flags().StringVar(&commitMode, "commit-mode", commitModeCheckpoint, "Commit target: checkpoint, direct, sync")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait for another checkpoint operation to finish (0 waits indefinitely)")

	return cmd
}
