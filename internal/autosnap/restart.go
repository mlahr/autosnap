package autosnap

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newRestartCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Restart the autosnap daemon with the current configuration",
		Long: `Restart the running autosnap daemon with the current .autosnap.toml.

Configuration flags supplied to the original autosnap start remain overrides.
To change those overrides, run autosnap stop and then autosnap start with the
new flags. Detailed restart progress is appended to the autosnap daemon log.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, _, _, err := detectRepository(context.Background())
			if err != nil {
				return err
			}
			lockPath, err := daemonStartLockPath(repoRoot)
			if err != nil {
				return err
			}
			lock, err := acquireFileLock(context.Background(), lockPath, daemonStartLockTimeout, "daemon start")
			if err != nil {
				return err
			}
			defer lock.Close()

			runPath, err := runStatePath(repoRoot)
			if err != nil {
				return err
			}
			runState, err := loadAutosnapRunState(runPath)
			if err != nil {
				return err
			}
			if err := validateRestartRunState(runState); err != nil {
				return err
			}

			logFile, logPath, err := openRestartLog(repoRoot)
			if err != nil {
				return err
			}
			defer logFile.Close()
			if err := writeRestartLog(logFile, "restart requested; active_pid=%d", runState.PID); err != nil {
				return err
			}
			if err := writeRestartLog(logFile, "loading configuration; path=%s; preserved_start_flags=%s", autosnapConfigPath(repoRoot), formatStartConfigFlags(runState.StartConfigFlags)); err != nil {
				return err
			}

			cfg, configFound, err := resolveRestartConfig(repoRoot, runState)
			if err != nil {
				_ = writeRestartLog(logFile, "configuration validation failed; error=%v", err)
				return err
			}
			configSource := "built-in defaults"
			if configFound {
				configSource = autosnapConfigPath(repoRoot)
			}
			if err := writeRestartLog(logFile, "configuration validated; source=%s; check=configured; msg_source_cmd=%s; msg_body_source_cmd=%s; notes=%s; post_checkpoint_command=%s; idle_seconds=%d; snapshot_mode=%s; commit_mode=%s; watch_mode=%s; poll_interval=%s; log_max_bytes=%d; ready_timeout=%s", configSource, enabledState(cfg.MsgSourceCmd != ""), enabledState(cfg.MsgBodySourceCmd != ""), enabledState(cfg.NoteCommand != ""), enabledState(cfg.PostCheckpointCommand != ""), cfg.IdleSeconds, cfg.SnapshotMode, cfg.CommitMode, cfg.Watch.Mode, cfg.Watch.PollInterval, cfg.LogMaxBytes, cfg.ReadyTimeout); err != nil {
				return err
			}

			runToken, err := newRunToken()
			if err != nil {
				_ = writeRestartLog(logFile, "run token generation failed; error=%v", err)
				return err
			}
			if err := writeRestartLog(logFile, "stopping daemon; pid=%d", runState.PID); err != nil {
				return err
			}

			if _, err := stopAutosnapRun(repoRoot, cmd.OutOrStdout()); err != nil {
				_ = writeRestartLog(logFile, "daemon stop failed; pid=%d; error=%v", runState.PID, err)
				return err
			}
			writeRestartLogAfterStop(cmd.ErrOrStderr(), logFile, logPath, "daemon stopped; pid=%d", runState.PID)
			writeRestartLogAfterStop(cmd.ErrOrStderr(), logFile, logPath, "starting replacement daemon")

			replacementProcess, err := startAutosnapDetached(repoRoot, cfg.Check, cfg.MsgSourceCmd, cfg.MsgBodySourceCmd, cfg.NoteCommand, cfg.NoteRef, cfg.PostCheckpointCommand, cfg.IdleSeconds, cfg.SnapshotMode, cfg.CommitMode, cfg.Watch.Mode, cfg.Watch.PollInterval, cfg.LogMaxBytes, runToken, runState.StartConfigFlags)
			if err != nil {
				writeRestartLogAfterStop(cmd.ErrOrStderr(), logFile, logPath, "replacement daemon start failed; error=%v", err)
				return err
			}
			if err := awaitStartedDaemon(context.Background(), repoRoot, runToken, replacementProcess, cfg.ReadyTimeout); err != nil {
				writeRestartLogAfterStop(cmd.ErrOrStderr(), logFile, logPath, "replacement daemon did not become ready; pid=%d; error=%v", replacementProcess.Pid, err)
				return err
			}
			writeRestartLogAfterStop(cmd.ErrOrStderr(), logFile, logPath, "replacement daemon started; pid=%d", replacementProcess.Pid)
			return nil
		},
	}
}

func validateRestartRunState(runState autosnapRunState) error {
	if runState.PID == 0 || !isAutosnapRunActive(runState) {
		return fmt.Errorf("autosnap daemon is not running; use 'autosnap start'")
	}
	if runState.StartConfigFlags == nil {
		return fmt.Errorf("cannot restart daemon started by an older autosnap version; run 'autosnap stop' then 'autosnap start'")
	}
	return nil
}

func resolveRestartConfig(repoRoot string, runState autosnapRunState) (autosnapConfig, bool, error) {
	set, err := configFlagSet(runState.StartConfigFlags)
	if err != nil {
		return autosnapConfig{}, false, err
	}
	overrides := autosnapConfigOverrides{
		values: autosnapConfig{
			Check:                 runState.CheckCommand,
			MsgSourceCmd:          runState.MsgSourceCmd,
			MsgBodySourceCmd:      runState.MsgBodySourceCmd,
			NoteCommand:           runState.NoteCommand,
			NoteRef:               runState.NoteRef,
			PostCheckpointCommand: runState.PostCheckpointCommand,
			IdleSeconds:           runState.IdleSeconds,
			SnapshotMode:          runState.SnapshotMode,
			CommitMode:            runState.CommitMode,
			LogMaxBytes:           runState.LogMaxBytes,
			Watch: autosnapWatchConfig{
				Mode:         runState.WatchMode,
				PollInterval: runState.PollInterval,
			},
		},
		set: set,
	}
	return resolveAutosnapConfig(repoRoot, overrides, true)
}

func openRestartLog(repoRoot string) (*os.File, string, error) {
	logPath, err := backgroundLogPath(repoRoot)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, "", err
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, "", fmt.Errorf("open restart log: %w", err)
	}
	return file, logPath, nil
}

func writeRestartLog(out io.Writer, format string, args ...any) error {
	allArgs := make([]any, 0, len(args)+1)
	allArgs = append(allArgs, time.Now().Format(time.RFC3339Nano))
	allArgs = append(allArgs, args...)
	if _, err := fmt.Fprintf(out, "[%s] restart: "+format+"\n", allArgs...); err != nil {
		return fmt.Errorf("write restart log: %w", err)
	}
	return nil
}

func writeRestartLogAfterStop(stderr io.Writer, logFile io.Writer, logPath, format string, args ...any) {
	if err := writeRestartLog(logFile, format, args...); err != nil {
		fmt.Fprintf(stderr, "warning: %v (log=%s)\n", err, logPath)
	}
}

func formatStartConfigFlags(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	formatted := make([]string, len(names))
	for i, name := range names {
		formatted[i] = "--" + name
	}
	return strings.Join(formatted, ",")
}

func enabledState(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}
