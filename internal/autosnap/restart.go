package autosnap

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newRestartCommand() *cobra.Command {
	var (
		checkCommand string
		msgSourceCmd string
		idleSeconds  int
		snapshotMode string
		commitMode   string
		watchMode    string
		pollInterval time.Duration
		logMaxBytes  int64
	)

	cmd := &cobra.Command{
		Use:   "restart",
		Short: "Restart the autosnap daemon",
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
			runState, err := loadAutosnapRunState(runPath)
			if err != nil {
				return err
			}
			active := runState.PID != 0 && isAutosnapRunActive(runState)

			cfg, err := resolveRestartConfig(repoRoot, cmd, runState, active, checkCommand, msgSourceCmd, idleSeconds, snapshotMode, commitMode, watchMode, pollInterval, logMaxBytes)
			if err != nil {
				return err
			}

			if _, err := stopAutosnapRun(repoRoot, cmd.OutOrStdout()); err != nil {
				return err
			}

			runToken, err := newRunToken()
			if err != nil {
				return err
			}
			return startAutosnapDetached(repoRoot, cfg.Check, cfg.MsgSourceCmd, cfg.NoteCommand, cfg.NoteRef, cfg.IdleSeconds, cfg.SnapshotMode, cfg.CommitMode, cfg.Watch.Mode, cfg.Watch.PollInterval, cfg.LogMaxBytes, runToken)
		},
	}

	cmd.Flags().StringVar(&checkCommand, "check", "", "Shell command to run after idle")
	cmd.Flags().StringVar(&msgSourceCmd, "msg-source-cmd", "", "Shell command that returns the checkpoint commit message (multiline supported)")
	cmd.Flags().String("note-command", "", "Shell command that returns the checkpoint git note content")
	cmd.Flags().String("note-ref", "", "Git notes ref for checkpoint notes")
	cmd.Flags().IntVar(&idleSeconds, "idle", 60, "Seconds without changes before running the check")
	cmd.Flags().StringVar(&snapshotMode, "snapshot-mode", snapshotModeBoth, "Snapshot source: both, staged, working")
	cmd.Flags().StringVar(&commitMode, "commit-mode", commitModeCheckpoint, "Commit target: checkpoint, direct, sync")
	cmd.Flags().StringVar(&watchMode, "watch-mode", watchModeRecursive, "Watch strategy: recursive, poll, auto")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", defaultPollInterval, "Polling interval for poll or auto watch mode")
	cmd.Flags().Int64Var(&logMaxBytes, "log-max-bytes", defaultLogMaxBytes, "Maximum autosnap daemon log size in bytes")

	return cmd
}

func resolveRestartConfig(repoRoot string, cmd *cobra.Command, runState autosnapRunState, useRunState bool, checkCommand, msgSourceCmd string, idleSeconds int, snapshotMode, commitMode, watchMode string, pollInterval time.Duration, logMaxBytes int64) (autosnapConfig, error) {
	if !useRunState {
		cfg, _, err := resolveStartConfig(repoRoot, cmd, checkCommand, msgSourceCmd, idleSeconds, snapshotMode, commitMode, watchMode, pollInterval, logMaxBytes)
		return cfg, err
	}

	cfg := defaultAutosnapConfig()
	fileCfg, found, err := loadAutosnapConfig(repoRoot)
	if err != nil {
		return cfg, err
	}
	if found {
		mergeAutosnapConfig(&cfg, fileCfg)
	}

	if strings.TrimSpace(runState.CheckCommand) != "" {
		cfg.Check = runState.CheckCommand
	}
	if runState.MsgSourceCmdSet {
		cfg.MsgSourceCmd = runState.MsgSourceCmd
	}
	if strings.TrimSpace(runState.NoteCommand) != "" {
		cfg.NoteCommand = runState.NoteCommand
	}
	if strings.TrimSpace(runState.NoteRef) != "" {
		cfg.NoteRef = runState.NoteRef
	}
	if runState.IdleSeconds != 0 {
		cfg.IdleSeconds = runState.IdleSeconds
	}
	if strings.TrimSpace(runState.SnapshotMode) != "" {
		cfg.SnapshotMode = runState.SnapshotMode
	}
	if strings.TrimSpace(runState.CommitMode) != "" {
		cfg.CommitMode = runState.CommitMode
	}
	if strings.TrimSpace(runState.WatchMode) != "" {
		cfg.Watch.Mode = runState.WatchMode
	}
	if runState.PollInterval != 0 {
		cfg.Watch.PollInterval = runState.PollInterval
	}
	if runState.LogMaxBytes != 0 {
		cfg.LogMaxBytes = runState.LogMaxBytes
	}

	flags := cmd.Flags()
	if flags.Changed("check") {
		cfg.Check = checkCommand
	}
	if flags.Changed("msg-source-cmd") {
		cfg.MsgSourceCmd = msgSourceCmd
	}
	if flags.Changed("note-command") {
		cfg.NoteCommand = noteCommandFlag(cmd)
	}
	if flags.Changed("note-ref") {
		cfg.NoteRef = noteRefFlag(cmd)
	}
	if flags.Changed("idle") {
		cfg.IdleSeconds = idleSeconds
	}
	if flags.Changed("snapshot-mode") {
		cfg.SnapshotMode = snapshotMode
	}
	if flags.Changed("commit-mode") {
		cfg.CommitMode = commitMode
	}
	if flags.Changed("watch-mode") {
		cfg.Watch.Mode = watchMode
	}
	if flags.Changed("poll-interval") {
		cfg.Watch.PollInterval = pollInterval
	}
	if flags.Changed("log-max-bytes") {
		cfg.LogMaxBytes = logMaxBytes
	}

	cfg.Check = strings.TrimSpace(cfg.Check)
	cfg.MsgSourceCmd = strings.TrimSpace(cfg.MsgSourceCmd)
	cfg.NoteCommand = strings.TrimSpace(cfg.NoteCommand)
	cfg.NoteRef = strings.TrimSpace(cfg.NoteRef)
	cfg.SnapshotMode = strings.TrimSpace(cfg.SnapshotMode)
	cfg.CommitMode = strings.TrimSpace(cfg.CommitMode)
	cfg.Watch.Mode = strings.TrimSpace(cfg.Watch.Mode)

	if err := validateStartConfig(cfg, flags.Changed("idle"), flags.Changed("poll-interval"), flags.Changed("log-max-bytes")); err != nil {
		return cfg, err
	}

	normalizedSnapshotMode, err := normalizeSnapshotMode(cfg.SnapshotMode)
	if err != nil {
		if flags.Changed("snapshot-mode") {
			return cfg, fmt.Errorf("invalid --snapshot-mode %q (expected both, staged, working)", cfg.SnapshotMode)
		}
		return cfg, fmt.Errorf("invalid snapshot_mode %q (expected both, staged, working)", cfg.SnapshotMode)
	}
	cfg.SnapshotMode = normalizedSnapshotMode

	normalizedCommitMode, err := normalizeCommitMode(cfg.CommitMode)
	if err != nil {
		if flags.Changed("commit-mode") {
			return cfg, fmt.Errorf("invalid --commit-mode %q (expected checkpoint, direct, sync)", cfg.CommitMode)
		}
		return cfg, fmt.Errorf("invalid commit_mode %q (expected checkpoint, direct, sync)", cfg.CommitMode)
	}
	cfg.CommitMode = normalizedCommitMode
	if isDirectCommitMode(cfg.CommitMode) && cfg.SnapshotMode != snapshotModeBoth {
		return cfg, fmt.Errorf("commit_mode %s requires snapshot_mode both", cfg.CommitMode)
	}

	normalizedWatchMode, err := normalizeWatchMode(cfg.Watch.Mode)
	if err != nil {
		if flags.Changed("watch-mode") {
			return cfg, fmt.Errorf("invalid --watch-mode %q (expected recursive, poll, auto)", cfg.Watch.Mode)
		}
		return cfg, fmt.Errorf("invalid watch.mode %q (expected recursive, poll, auto)", cfg.Watch.Mode)
	}
	cfg.Watch.Mode = normalizedWatchMode

	return cfg, nil
}
