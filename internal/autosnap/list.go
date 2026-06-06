package autosnap

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newListCommand() *cobra.Command {
	var (
		allBranches bool
		branch      string
	)

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List checkpoints",
		Args:  cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := context.Background()
			repoRoot, _, branchRef, err := detectRepository(ctx)
			if err != nil {
				return err
			}

			if strings.TrimSpace(branch) != "" && allBranches {
				return fmt.Errorf("list accepts at most one scope flag: --branch or --all")
			}

			var checkpoints []checkpointInfo
			switch {
			case strings.TrimSpace(branch) != "":
				checkpoints, err = listCheckpointsForBranch(ctx, repoRoot, strings.TrimSpace(branch))
			case allBranches:
				refs, refsErr := listCheckpointRefsForAllBranches(ctx, repoRoot)
				if refsErr != nil {
					return refsErr
				}
				checkpoints, err = listCheckpointsFromRefs(ctx, repoRoot, refs)
			default:
				checkpoints, err = listCheckpointsForBranch(ctx, repoRoot, branchRef)
			}
			if err != nil {
				return err
			}
			if len(checkpoints) == 0 {
				switch {
				case strings.TrimSpace(branch) != "":
					fmt.Fprintf(out, "no checkpoints for branch %s\n", strings.TrimSpace(branch))
				case allBranches:
					fmt.Fprintln(out, "no checkpoints")
				default:
					fmt.Fprintln(out, "no checkpoints for current branch")
				}
				return nil
			}

			for _, cp := range checkpoints {
				displayTimestamp := formatCheckpointTimestampForList(cp.Timestamp)
				if allBranches {
					fmt.Fprintf(out, "%s %s %s %s\n", cp.Branch, displayTimestamp, cp.Commit, cp.Summary)
				} else {
					fmt.Fprintf(out, "%s %s %s\n", displayTimestamp, cp.Commit, cp.Summary)
				}
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&branch, "branch", "", "List checkpoints for a specific branch")
	cmd.Flags().BoolVar(&allBranches, "all", false, "List checkpoints for all branches")
	return cmd
}

func formatCheckpointTimestampForList(timestamp string) string {
	return formatCheckpointTimestamp(timestamp)
}
