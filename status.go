package main

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
			ctx := context.Background()
			repoRoot, branchDisplay, branchRef, err := detectRepository(ctx)
			if err != nil {
				return err
			}

			statePath, err := stateFilePath(repoRoot)
			if err != nil {
				return err
			}
			state, _ := loadAutosnapState(statePath)

			lastCheckpoint := "none"
			if state.LastBranch == branchRef && state.LastCheckpointAt != "" {
				lastCheckpoint = state.LastCheckpointAt
			} else {
				ref, ts, _, err := getLatestCheckpointForBranch(ctx, repoRoot, branchRef)
				if err == nil && ref != "" && ts != "" {
					lastCheckpoint = ts
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

			pend, _ := hasWorkingTreeChanges(ctx, repoRoot)
			pending := "no"
			if pend {
				pending = "yes"
			}

			fmt.Printf("repo: %s\n", repoRoot)
			fmt.Printf("branch: %s\n", branchDisplay)
			fmt.Printf("last checkpoint: %s\n", lastCheckpoint)
			fmt.Printf("last check: %s\n", lastCheck)
			fmt.Printf("last check duration: %s\n", lastCheckDuration)
			if state.LastFailureAt != "" {
				fmt.Printf("last failed check: %s exit: %d\n", state.LastFailureAt, state.LastFailureExitCode)
			}
			fmt.Printf("pending changes: %s\n", pending)

			return nil
		},
	}
}

func hasWorkingTreeChanges(ctx context.Context, repoRoot string) (bool, error) {
	result, err := runGitCommand(ctx, repoRoot, nil, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(result.Stdout) != "", nil
}
