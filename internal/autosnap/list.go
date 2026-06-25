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
		Use:   "list [checkpoint-or-range]",
		Short: "List checkpoints",
		Long: strings.TrimSpace(`List checkpoints.

A range A..B lists checkpoints from A through B, inclusive. Ranges are
inclusive autosnap checkpoint intervals, not general Git revision ranges.

The checkpoint argument can be an explicit autosnap ref, a checkpoint commit hash,
or one of these current-branch history selectors:

  first
  first+N
  last
  last-N`),
		Args: cobra.MaximumNArgs(1),
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
			if len(args) > 0 && allBranches {
				return fmt.Errorf("list accepts checkpoint ranges only for current branch or --branch")
			}

			var checkpoints []checkpointInfo
			if len(args) > 0 {
				scopeBranch := branchRef
				if strings.TrimSpace(branch) != "" {
					scopeBranch = strings.TrimSpace(branch)
				}
				refs, refsErr := listCheckpointRefsForRange(ctx, repoRoot, scopeBranch, args[0])
				if refsErr != nil {
					return refsErr
				}
				checkpoints, err = listCheckpointsFromRefs(ctx, repoRoot, refs)
			} else {
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

func listCheckpointRefsForRange(ctx context.Context, repoRoot, branchRef, arg string) ([]checkpointRefInfo, error) {
	startArg, endArg, ranged, err := splitCheckpointRangeArg(arg)
	if err != nil {
		return nil, err
	}

	start, err := resolveShowCheckpointRefMetadata(ctx, repoRoot, branchRef, startArg)
	if err != nil {
		return nil, err
	}

	end := start
	if ranged {
		end, err = resolveShowCheckpointRefMetadata(ctx, repoRoot, branchRef, endArg)
		if err != nil {
			return nil, err
		}
	}

	startBranch := checkpointRefBranch(start, branchRef)
	endBranch := checkpointRefBranch(end, branchRef)
	if startBranch != endBranch {
		return nil, fmt.Errorf("checkpoint range endpoints must be on the same autosnap branch: %s and %s", startBranch, endBranch)
	}

	checkpoints, err := listCheckpointRefsForBranch(ctx, repoRoot, startBranch)
	if err != nil {
		return nil, err
	}

	startIndex := checkpointRefIndex(checkpoints, start.Ref)
	if startIndex < 0 {
		return nil, fmt.Errorf("checkpoint not found in branch history: %s", start.Ref)
	}
	endIndex := checkpointRefIndex(checkpoints, end.Ref)
	if endIndex < 0 {
		return nil, fmt.Errorf("checkpoint not found in branch history: %s", end.Ref)
	}
	if startIndex > endIndex {
		return nil, fmt.Errorf("checkpoint range start must not be after range end")
	}

	return append([]checkpointRefInfo(nil), checkpoints[startIndex:endIndex+1]...), nil
}

func formatCheckpointTimestampForList(timestamp string) string {
	return formatCheckpointTimestamp(timestamp)
}
