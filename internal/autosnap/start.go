package autosnap

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"syscall"

	"github.com/spf13/cobra"
)

func newStartCommand() *cobra.Command {
	var (
		checkCommand string
		msgSourceCmd string
		idleSeconds  int
		snapshotMode string
		commitMode   string
		watchMode    string
		pollInterval time.Duration
		foreground   bool
		daemon       bool
		runToken     string
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start autosnap watcher and checkpoint on idle passing checks",
		RunE: func(cmd *cobra.Command, args []string) error {
			repoRoot, branchDisplay, branchRef, err := detectRepository(context.Background())
			if err != nil {
				return err
			}

			cfg, _, err := resolveStartConfig(repoRoot, cmd, checkCommand, msgSourceCmd, idleSeconds, snapshotMode, commitMode, watchMode, pollInterval)
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

			if err := ensureNoActiveRunForRepo(repoRoot); err != nil {
				return err
			}

			if !foreground {
				if runToken == "" {
					runToken, err = newRunToken()
					if err != nil {
						return err
					}
				}
				return startAutosnapDetached(repoRoot, checkCommand, msgSourceCmd, idleSeconds, snapshotMode, commitMode, watchMode, pollInterval, runToken)
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

			if daemon {
				runPath, err := runStatePath(repoRoot)
				if err != nil {
					return err
				}

				runState := autosnapRunState{
					PID:             os.Getpid(),
					RepoRoot:        repoRoot,
					BranchRef:       branchRef,
					BranchDisplay:   branchDisplay,
					CheckCommand:    checkCommand,
					MsgSourceCmd:    msgSourceCmd,
					MsgSourceCmdSet: true,
					IdleSeconds:     idleSeconds,
					SnapshotMode:    snapshotMode,
					CommitMode:      commitMode,
					WatchMode:       watchMode,
					PollInterval:    pollInterval,
					RunToken:        runToken,
					StartedAt:       time.Now().UTC().Format(time.RFC3339),
				}
				if err := saveAutosnapRunState(runPath, runState); err != nil {
					return err
				}
				defer removeAutosnapRunState(runPath)
			}

			logf("autosnap watching %s\n", repoRoot)
			logf("branch: %s check: %s idle: %ds\n", branchDisplay, checkCommand, idleSeconds)
			logf("snapshot-mode: %s commit-mode: %s\n", snapshotMode, commitMode)
			logf("watch-mode: %s poll-interval: %s\n", watchMode, pollInterval)

			if err := runner.start(); err != nil {
				cancel()
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&checkCommand, "check", "", "Shell command to run after idle")
	cmd.Flags().StringVar(&msgSourceCmd, "msg-source-cmd", "", "Shell command that returns the checkpoint commit message (multiline supported)")
	cmd.Flags().IntVar(&idleSeconds, "idle", 60, "Seconds without changes before running the check")
	cmd.Flags().StringVar(&snapshotMode, "snapshot-mode", snapshotModeBoth, "Snapshot source: both, staged, working")
	cmd.Flags().StringVar(&commitMode, "commit-mode", commitModeCheckpoint, "Commit target: checkpoint, direct, sync")
	cmd.Flags().StringVar(&watchMode, "watch-mode", watchModeRecursive, "Watch strategy: recursive, poll, auto")
	cmd.Flags().DurationVar(&pollInterval, "poll-interval", defaultPollInterval, "Polling interval for poll or auto watch mode")
	cmd.Flags().BoolVar(&foreground, "foreground", false, "Run autosnap in the current terminal")
	cmd.Flags().StringVar(&runToken, "run-token", "", "Internal: run identity token")
	cmd.Flags().BoolVar(&daemon, "daemon", false, "Internal: run as background daemon")
	cmd.Flags().Lookup("run-token").Hidden = true
	cmd.Flags().Lookup("daemon").Hidden = true

	return cmd
}

func startAutosnapDetached(repoRoot, checkCommand, msgSourceCmd string, idleSeconds int, snapshotMode, commitMode, watchMode string, pollInterval time.Duration, runToken string) error {
	logPath, err := backgroundLogPath(repoRoot)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	exe, err := os.Executable()
	if err != nil {
		return err
	}

	args := startDetachedArgs(exe, checkCommand, msgSourceCmd, idleSeconds, snapshotMode, commitMode, watchMode, pollInterval, runToken)

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = append(os.Environ(), autosnapTimestampLogEnv+"=1")
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	fmt.Printf("autosnap started in background (pid=%d, log=%s)\n", cmd.Process.Pid, logPath)
	return nil
}

func startDetachedArgs(exe, checkCommand, msgSourceCmd string, idleSeconds int, snapshotMode, commitMode, watchMode string, pollInterval time.Duration, runToken string) []string {
	args := []string{
		exe,
		"start",
		"--foreground",
		"--daemon",
		"--check",
		checkCommand,
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
		"--run-token",
		runToken,
	}

	if msgSourceCmd != "" {
		args = append(args, "--msg-source-cmd", msgSourceCmd)
	}
	return args
}

func backgroundLogPath(repoRoot string) (string, error) {
	statePath, err := stateFilePath(repoRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(statePath), "autosnap.log"), nil
}
