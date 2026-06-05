package autosnap

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newPendingCommand() *cobra.Command {
	var (
		allBranches bool
		branch      string
	)

	cmd := &cobra.Command{
		Use:   "pending",
		Short: "List checkpoints after the latest checkpoint matching branch tip",
		Args:  cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := context.Background()
			repoRoot, _, branchRef, err := detectRepository(ctx)
			if err != nil {
				return err
			}

			scopeCount := 0
			if strings.TrimSpace(branch) != "" {
				scopeCount++
			}
			if allBranches {
				scopeCount++
			}
			if scopeCount > 1 {
				return fmt.Errorf("%s accepts at most one scope flag: --branch or --all", cmd.CalledAs())
			}

			var refs []checkpointRefInfo
			switch {
			case strings.TrimSpace(branch) != "":
				refs, err = listCheckpointRefsForBranch(ctx, repoRoot, strings.TrimSpace(branch))
			case allBranches:
				refs, err = listCheckpointRefsForAllBranches(ctx, repoRoot)
			default:
				refs, err = listCheckpointRefsForBranch(ctx, repoRoot, branchRef)
			}
			if err != nil {
				return err
			}

			checkpoints, err := listCheckpointsFromRefs(ctx, repoRoot, refs)
			if err != nil {
				return err
			}

			if len(checkpoints) == 0 {
				fmt.Fprintln(out, "no checkpoints")
				return nil
			}

			pending, err := filterPendingCheckpointRefs(ctx, repoRoot, checkpoints, branch, allBranches)
			if err != nil {
				return err
			}

			if len(pending) == 0 {
				if allBranches {
					fmt.Fprintln(out, "no pending checkpoints")
				} else if strings.TrimSpace(branch) != "" {
					fmt.Fprintf(out, "no pending checkpoints for branch %s\n", strings.TrimSpace(branch))
				} else {
					fmt.Fprintln(out, "no pending checkpoints for current branch")
				}
				return nil
			}

			for _, cp := range pending {
				displayTimestamp := formatCheckpointTimestampForList(cp.Timestamp)
				if allBranches {
					fmt.Fprintf(out, "%s %s %s %s %s\n", cp.Branch, displayTimestamp, cp.Ref, cp.Commit, cp.Summary)
				} else {
					fmt.Fprintf(out, "%s %s %s %s\n", displayTimestamp, cp.Ref, cp.Commit, cp.Summary)
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&branch, "branch", "", "List pending checkpoints for a specific branch")
	cmd.Flags().BoolVar(&allBranches, "all", false, "List pending checkpoints for all branches")
	return cmd
}

func filterPendingCheckpointRefs(ctx context.Context, repoRoot string, checkpoints []checkpointInfo, branch string, allBranches bool) ([]checkpointInfo, error) {
	grouped := map[string][]checkpointInfo{}
	branchOrder := []string{}
	for _, cp := range checkpoints {
		baseRef := strings.TrimSpace(branch)
		if allBranches || baseRef == "" {
			baseRef = cp.Branch
		}
		baseRef = strings.TrimSpace(baseRef)
		if baseRef == "" {
			continue
		}
		if _, ok := grouped[baseRef]; !ok {
			branchOrder = append(branchOrder, baseRef)
		}
		grouped[baseRef] = append(grouped[baseRef], cp)
	}

	pending := make([]checkpointInfo, 0, len(checkpoints))
	for _, baseRef := range branchOrder {
		resolvedRef := baseRef
		if strings.HasPrefix(resolvedRef, "detached-") {
			resolvedRef = "HEAD"
		}

		result, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", resolvedRef)
		if err != nil {
			if allBranches {
				continue
			}
			return nil, fmt.Errorf("failed to resolve branch tip %q: %w", resolvedRef, gitCommandError(err, result))
		}
		if strings.TrimSpace(result.Stdout) == "" {
			if allBranches {
				continue
			}
			return nil, fmt.Errorf("failed to resolve branch tip %q", resolvedRef)
		}

		syncedIndex := -1
		for i, cp := range grouped[baseRef] {
			synced, err := checkpointMatchesRef(ctx, repoRoot, resolvedRef, cp.Commit)
			if err != nil {
				return nil, err
			}
			if synced {
				syncedIndex = i
			}
		}
		pending = append(pending, grouped[baseRef][syncedIndex+1:]...)
	}

	return pending, nil
}

func checkpointMatchesRef(ctx context.Context, repoRoot, baseRef, checkpointCommit string) (bool, error) {
	result, err := runGitCommand(ctx, repoRoot, nil, "diff", "--quiet", baseRef, checkpointCommit)
	if err == nil {
		return true, nil
	}
	if result.ExitCode == 1 {
		return false, nil
	}
	return false, gitCommandError(err, result)
}
