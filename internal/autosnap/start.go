package autosnap

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"syscall"

	"github.com/spf13/cobra"
)

func newStartCommand() *cobra.Command {
	var (
		checkCommand          string
		msgSourceCmd          string
		idleSeconds           int
		snapshotMode          string
		commitMode            string
		watchMode             string
		pollInterval          time.Duration
		logMaxBytes           int64
		foreground            bool
		daemon                bool
		runToken              string
		resolvedConfig        bool
		startConfigFlagsValue string
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start the autosnap daemon and checkpoint on idle passing checks",
		RunE: func(cmd *cobra.Command, args []string) error {
			startConfigFlags := configFlagNames(cmd)
			if cmd.Flags().Changed("start-config-flags") {
				parsedFlags, parseErr := parseStartConfigFlags(startConfigFlagsValue)
				if parseErr != nil {
					return parseErr
				}
				startConfigFlags = parsedFlags
			}
			if resolvedConfig && !cmd.Flags().Changed("start-config-flags") {
				return fmt.Errorf("internal --resolved-config requires --start-config-flags")
			}

			repoRoot, branchDisplay, branchRef, err := detectRepository(context.Background())
			if err != nil {
				return err
			}

			cfg, _, err := resolveStartConfigWithFile(repoRoot, cmd, checkCommand, msgSourceCmd, idleSeconds, snapshotMode, commitMode, watchMode, pollInterval, logMaxBytes, !resolvedConfig)
			if err != nil {
				return err
			}
			checkCommand = cfg.Check
			msgSourceCmd = cfg.MsgSourceCmd
			idleSeconds = cfg.IdleSeconds
			snapshotMode = cfg.SnapshotMode
			commitMode = cfg.CommitMode
			watchMode = cfg.Watch.Mode
			pollInterval = cfg.Watch.PollInterval
			logMaxBytes = cfg.LogMaxBytes
			noteCommand := cfg.NoteCommand
			noteRef := cfg.NoteRef
			postCheckpointCommand := cfg.PostCheckpointCommand

			if !foreground {
				lockPath, err := daemonStartLockPath(repoRoot)
				if err != nil {
					return err
				}
				lock, err := acquireFileLock(context.Background(), lockPath, daemonStartLockTimeout, "daemon start")
				if err != nil {
					return err
				}
				defer lock.Close()
				if err := ensureNoActiveRunForRepo(repoRoot); err != nil {
					return err
				}
				if runToken == "" {
					runToken, err = newRunToken()
					if err != nil {
						return err
					}
				}
				process, err := startAutosnapDetached(repoRoot, checkCommand, msgSourceCmd, noteCommand, noteRef, postCheckpointCommand, idleSeconds, snapshotMode, commitMode, watchMode, pollInterval, logMaxBytes, runToken, startConfigFlags)
				if err != nil {
					return err
				}
				if err := awaitStartedDaemon(context.Background(), repoRoot, runToken, process, daemonReadyTimeout); err != nil {
					return err
				}
				return nil
			}

			if err := ensureNoActiveRunForRepo(repoRoot); err != nil {
				return err
			}

			if runToken == "" {
				runToken, err = newRunToken()
				if err != nil {
					return err
				}
			}

			ctx, cancel := context.WithCancel(context.Background())
			waitUntilSignal(cancel)

			statePath, err := stateFilePath(repoRoot)
			if err != nil {
				return err
			}

			runner, err := newSnapshotRunnerWithWatch(ctx, repoRoot, branchRef, checkCommand, msgSourceCmd, snapshotMode, commitMode, watchMode, pollInterval, time.Duration(idleSeconds)*time.Second, statePath)
			if err != nil {
				return err
			}
			runner.noteCommand = noteCommand
			runner.noteRef = noteRef
			runner.postCheckpointCommand = postCheckpointCommand

			var runPath string
			var runState autosnapRunState
			if daemon {
				runPath, err = runStatePath(repoRoot)
				if err != nil {
					return err
				}

				runState = autosnapRunState{
					PID:                   os.Getpid(),
					RepoRoot:              repoRoot,
					BranchRef:             branchRef,
					BranchDisplay:         branchDisplay,
					CheckCommand:          checkCommand,
					MsgSourceCmd:          msgSourceCmd,
					MsgSourceCmdSet:       true,
					NoteCommand:           noteCommand,
					NoteRef:               noteRef,
					PostCheckpointCommand: postCheckpointCommand,
					IdleSeconds:           idleSeconds,
					SnapshotMode:          snapshotMode,
					CommitMode:            commitMode,
					WatchMode:             watchMode,
					PollInterval:          pollInterval,
					LogMaxBytes:           logMaxBytes,
					StartConfigFlags:      append([]string{}, startConfigFlags...),
					RunToken:              runToken,
					StartedAt:             time.Now().UTC().Format(time.RFC3339),
				}
				defer removeAutosnapRunState(runPath)
			}

			logf("autosnap watching %s\n", repoRoot)
			logf("branch: %s check: %s idle: %ds\n", branchDisplay, checkCommand, idleSeconds)
			logf("snapshot-mode: %s commit-mode: %s\n", snapshotMode, commitMode)
			logf("watch-mode: %s poll-interval: %s\n", watchMode, pollInterval)

			ready := func() error {
				if !daemon {
					return nil
				}
				if err := saveAutosnapRunState(runPath, runState); err != nil {
					return err
				}
				startLogCompactor(ctx, repoRoot, logMaxBytes, defaultLogCleanupInterval)
				return nil
			}
			if err := runner.start(ready); err != nil {
				cancel()
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&checkCommand, "check", "", "Shell command to run after idle")
	cmd.Flags().StringVar(&msgSourceCmd, "msg-source-cmd", "", "Shell command that returns the checkpoint commit message (multiline supported)")
	cmd.Flags().String("note-command", "", "Shell command that returns the checkpoint git note content")
	cmd.Flags().String("note-ref", "", "Git notes ref for checkpoint notes")
	cmd.Flags().String("post-checkpoint-command", "", "Shell command to run after creating a checkpoint")
	cmd.Flags().IntVar(&idleSeconds, "idle", 60, "Seconds without changes before running the check")
	cmd.Flags().StringVar(&snapshotMode, "snapshot-mode", snapshotModeBoth, "Snapshot source: both, staged, working")
	cmd.Flags().StringVar(&commitMode, "commit-mode", commitModeCheckpoint, "Commit target: checkpoint, direct, sync")
	cmd.Flags().StringVar(&watchMode, "watch-mode", watchModeRecursive, "Watch strategy: recursive, poll, auto")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", defaultPollInterval, "Polling interval for poll or auto watch mode")
	cmd.Flags().Int64Var(&logMaxBytes, "log-max-bytes", defaultLogMaxBytes, "Maximum autosnap daemon log size in bytes")
	cmd.Flags().BoolVar(&foreground, "foreground", false, "Run autosnap in the current terminal")
	cmd.Flags().StringVar(&runToken, "run-token", "", "Internal: run identity token")
	cmd.Flags().BoolVar(&daemon, "daemon", false, "Internal: run as background daemon")
	cmd.Flags().BoolVar(&resolvedConfig, "resolved-config", false, "Internal: use fully resolved configuration flags")
	cmd.Flags().StringVar(&startConfigFlagsValue, "start-config-flags", "", "Internal: original start configuration flags")
	cmd.Flags().Lookup("run-token").Hidden = true
	cmd.Flags().Lookup("daemon").Hidden = true
	cmd.Flags().Lookup("resolved-config").Hidden = true
	cmd.Flags().Lookup("start-config-flags").Hidden = true

	return cmd
}

func startAutosnapDetached(repoRoot, checkCommand, msgSourceCmd, noteCommand, noteRef, postCheckpointCommand string, idleSeconds int, snapshotMode, commitMode, watchMode string, pollInterval time.Duration, logMaxBytes int64, runToken string, startConfigFlags []string) (*os.Process, error) {
	logPath, err := backgroundLogPath(repoRoot)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, err
	}
	if err := compactLogFile(logPath, logMaxBytes); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	defer logFile.Close()

	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}

	args := startDetachedArgs(exe, checkCommand, msgSourceCmd, noteCommand, noteRef, postCheckpointCommand, idleSeconds, snapshotMode, commitMode, watchMode, pollInterval, logMaxBytes, runToken, startConfigFlags)

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), autosnapTimestampLogEnv+"=1")
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	fmt.Printf("autosnap started in background (pid=%d, log=%s)\n", cmd.Process.Pid, logPath)
	return cmd.Process, nil
}

func startDetachedArgs(exe, checkCommand, msgSourceCmd, noteCommand, noteRef, postCheckpointCommand string, idleSeconds int, snapshotMode, commitMode, watchMode string, pollInterval time.Duration, logMaxBytes int64, runToken string, startConfigFlags []string) []string {
	args := []string{
		exe,
		"start",
		"--foreground",
		"--daemon",
		"--resolved-config",
		"--start-config-flags",
		strings.Join(startConfigFlags, ","),
		"--check",
		checkCommand,
		"--msg-source-cmd",
		msgSourceCmd,
		"--note-command",
		noteCommand,
		"--note-ref",
		noteRef,
		"--post-checkpoint-command",
		postCheckpointCommand,
		"--snapshot-mode",
		snapshotMode,
		"--commit-mode",
		commitMode,
		"--watch-mode",
		watchMode,
		"--poll-interval",
		pollInterval.String(),
		"--idle",
		strconv.Itoa(idleSeconds),
		"--log-max-bytes",
		strconv.FormatInt(logMaxBytes, 10),
		"--run-token",
		runToken,
	}

	return args
}

func parseStartConfigFlags(raw string) ([]string, error) {
	requested := []string{}
	if raw != "" {
		requested = strings.Split(raw, ",")
	}
	set, err := configFlagSet(requested)
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(set))
	for _, name := range startConfigFlagNames {
		if set[name] {
			names = append(names, name)
		}
	}
	return names, nil
}

func backgroundLogPath(repoRoot string) (string, error) {
	statePath, err := stateFilePath(repoRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(statePath), "autosnap.log"), nil
}
