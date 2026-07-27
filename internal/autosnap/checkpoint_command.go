package autosnap

import (
	"context"
	"fmt"
	"strings"
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
		Use:   "checkpoint [COMMIT_MSG]",
		Short: "Create a checkpoint immediately",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if timeout < 0 {
				return fmt.Errorf("--timeout must be greater than or equal to 0")
			}
			commitMsg := ""
			if len(args) > 0 {
				commitMsg = strings.TrimSpace(args[0])
			}

			ctx := context.Background()
			repoRoot, _, branchRef, err := detectRepository(ctx)
			if err != nil {
				return err
			}

			cfg, _, err := resolveStartConfig(repoRoot, cmd, checkCommand, msgSourceCmd, defaultAutosnapConfig().IdleSeconds, snapshotMode, commitMode, watchModeRecursive, defaultPollInterval, defaultLogMaxBytes)
			if err != nil {
				return err
			}
			// An explicit positional message has priority over both the configured
			// and command-line message source. In particular, do not execute a
			// configured shell command when the user supplied COMMIT_MSG.
			if commitMsg != "" {
				cfg.MsgSourceCmd = ""
			}

			statePath, err := stateFilePath(repoRoot)
			if err != nil {
				return err
			}

			runner, err := newSnapshotRunnerWithWatch(ctx, repoRoot, branchRef, cfg.Check, cfg.MsgSourceCmd, cfg.SnapshotMode, cfg.CommitMode, cfg.Watch.Mode, cfg.Watch.PollInterval, time.Duration(cfg.IdleSeconds)*time.Second, statePath)
			if err != nil {
				return err
			}
			runner.commitMsg = commitMsg
			runner.noteCommand = cfg.NoteCommand
			runner.noteRef = cfg.NoteRef
			runner.failOnNoteError = true

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
	cmd.Flags().String("note-command", "", "Shell command that returns the checkpoint git note content")
	cmd.Flags().String("note-ref", "", "Git notes ref for checkpoint notes")
	cmd.Flags().StringVar(&snapshotMode, "snapshot-mode", snapshotModeBoth, "Snapshot source: both, staged, working")
	cmd.Flags().StringVar(&commitMode, "commit-mode", commitModeCheckpoint, "Commit target: checkpoint, direct, sync")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "Maximum time to wait for another checkpoint operation to finish (0 waits indefinitely)")

	return cmd
}

func validateCheckpointCommitMessageSources(commitMsg, msgSourceCmd string) error {
	if strings.TrimSpace(commitMsg) != "" && strings.TrimSpace(msgSourceCmd) != "" {
		return fmt.Errorf("COMMIT_MSG cannot be used with --msg-source-cmd")
	}
	return nil
}
