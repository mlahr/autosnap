package autosnap

import (
	"context"
	"errors"
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
		idleSeconds  int
		foreground   bool
		daemon       bool
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start autosnap watcher and checkpoint on idle passing checks",
		RunE: func(cmd *cobra.Command, args []string) error {
			if checkCommand == "" {
				return errors.New("--check is required")
			}
			if idleSeconds <= 0 {
				return errors.New("--idle must be greater than 0")
			}

			if !foreground {
				return startAutosnapDetached(checkCommand, idleSeconds)
			}

			repoRoot, branchDisplay, branchRef, err := detectRepository(context.Background())
			if err != nil {
				return err
			}

			ctx, cancel := context.WithCancel(context.Background())
			waitUntilSignal(cancel)

			statePath, err := stateFilePath(repoRoot)
			if err != nil {
				return err
			}

			runner, err := newSnapshotRunner(ctx, repoRoot, branchRef, checkCommand, time.Duration(idleSeconds)*time.Second, statePath)
			if err != nil {
				return err
			}

			if daemon {
				runPath, err := runStatePath(repoRoot)
				if err != nil {
					return err
				}

				runState := autosnapRunState{
					PID:           os.Getpid(),
					RepoRoot:      repoRoot,
					BranchRef:     branchRef,
					BranchDisplay: branchDisplay,
					CheckCommand:  checkCommand,
					IdleSeconds:   idleSeconds,
					StartedAt:     time.Now().UTC().Format(time.RFC3339),
				}
				if err := saveAutosnapRunState(runPath, runState); err != nil {
					return err
				}
				defer removeAutosnapRunState(runPath)
			}

			fmt.Printf("autosnap watching %s\n", repoRoot)
			fmt.Printf("branch: %s check: %s idle: %ds\n", branchDisplay, checkCommand, idleSeconds)

			if err := runner.start(); err != nil {
				cancel()
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&checkCommand, "check", "", "Shell command to run after idle")
	cmd.Flags().IntVar(&idleSeconds, "idle", 60, "Seconds without changes before running the check")
	cmd.Flags().BoolVar(&foreground, "foreground", false, "Run autosnap in the current terminal")
	cmd.Flags().BoolVar(&daemon, "daemon", false, "Internal: run as background daemon")
	cmd.Flags().Lookup("daemon").Hidden = true

	return cmd
}

func startAutosnapDetached(checkCommand string, idleSeconds int) error {
	repoRoot, _, _, err := detectRepository(context.Background())
	if err != nil {
		return err
	}

	if err := ensureNoActiveRunForRepo(repoRoot); err != nil {
		return err
	}

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

	cmd := exec.Command(
		exe,
		"start",
		"--foreground",
		"--daemon",
		"--check",
		checkCommand,
		"--idle",
		strconv.Itoa(idleSeconds),
	)
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()
	if runtime.GOOS != "windows" {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	fmt.Printf("autosnap started in background (pid=%d, log=%s)\n", cmd.Process.Pid, logPath)
	return nil
}

func backgroundLogPath(repoRoot string) (string, error) {
	statePath, err := stateFilePath(repoRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(statePath), "autosnap.log"), nil
}
