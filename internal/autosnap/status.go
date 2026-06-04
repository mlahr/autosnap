package autosnap

import (
	"context"
	"fmt"
	"github.com/spf13/cobra"
	"strings"
)

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show current autosnap state",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := context.Background()
			repoRoot, branchDisplay, branchRef, err := detectRepository(ctx)
			if err != nil {
				return err
			}

			statePath, err := stateFilePath(repoRoot)
			if err != nil {
				return err
			}
			state, err := loadAutosnapState(statePath)
			if err != nil {
				return fmt.Errorf("failed to load autosnap state: %w", err)
			}

			daemonStatus, err := getDaemonStatus(repoRoot)
			if err != nil {
				return fmt.Errorf("failed to read daemon status: %w", err)
			}

			lastCheckpoint := "none"
			if state.LastBranch == branchRef && state.LastCheckpointAt != "" {
				lastCheckpoint = state.LastCheckpointAt
			} else {
				ref, ts, _, err := getLatestCheckpointForBranch(ctx, repoRoot, branchRef)
				if err == nil && ref != "" && ts != "" {
					lastCheckpoint = ts
				} else if err != nil {
					return fmt.Errorf("failed to read latest checkpoint for branch: %w", err)
				}
			}

			lastCheck := "never"
			if state.LastCheckStatus != "" && state.LastBranch == branchRef {
				lastCheck = state.LastCheckStatus
			}

			lastCheckDuration := "n/a"
			if state.LastCheckStatus != "" && state.LastBranch == branchRef && state.LastCheckDurationMs > 0 {
				lastCheckDuration = fmt.Sprintf("%.1fs", float64(state.LastCheckDurationMs)/1000)
			}

			pend, err := hasWorkingTreeChanges(ctx, repoRoot)
			if err != nil {
				return fmt.Errorf("failed to check working-tree status: %w", err)
			}
			pending := "no"
			if pend {
				pending = "yes"
			}

			fmt.Fprintf(out, "repo: %s\n", repoRoot)
			fmt.Fprintf(out, "branch: %s\n", branchDisplay)
			fmt.Fprintf(out, "%s\n", daemonStatus)
			fmt.Fprintf(out, "last checkpoint: %s\n", lastCheckpoint)
			fmt.Fprintf(out, "last check: %s\n", lastCheck)
			fmt.Fprintf(out, "last check duration: %s\n", lastCheckDuration)
			if state.LastFailureAt != "" {
				fmt.Fprintf(out, "last failed check: %s exit: %d\n", state.LastFailureAt, state.LastFailureExitCode)
			}
			fmt.Fprintf(out, "pending changes: %s\n", pending)

			return nil
		},
	}
}

func getDaemonStatus(repoRoot string) (string, error) {
	runPath, err := runStatePath(repoRoot)
	if err != nil {
		return "", err
	}

	state, err := loadAutosnapRunState(runPath)
	if err != nil {
		return "", err
	}

	if state.PID == 0 {
		return "daemon: not running", nil
	}

	if !isProcessAlive(state.PID) {
		return fmt.Sprintf("daemon: stopped (stale pid=%d)", state.PID), nil
	}

	branch := state.BranchDisplay
	if branch == "" {
		branch = state.BranchRef
	}

	startAt := state.StartedAt
	if startAt == "" {
		startAt = "unknown"
	}

	return fmt.Sprintf("daemon: running (pid=%d branch=%s check=%q idle=%ds started=%s)", state.PID, branch, state.CheckCommand, state.IdleSeconds, startAt), nil
}

func hasWorkingTreeChanges(ctx context.Context, repoRoot string) (bool, error) {
	result, err := runGitCommand(ctx, repoRoot, nil, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(result.Stdout) != "", nil
}
