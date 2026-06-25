package autosnap

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/spf13/cobra"
)

func newUnpickCommand() *cobra.Command {
	var (
		force          bool
		conflictPolicy string
	)

	cmd := &cobra.Command{
		Use:   "unpick <checkpoint-or-range>",
		Short: "Remove a checkpoint's incremental patch",
		Long: strings.TrimSpace(`Remove the same patch displayed by autosnap show <checkpoint>.

A range A..B removes the net patch from the diff base of A through B. Ranges
are inclusive autosnap checkpoint intervals, not general Git revision ranges.

The checkpoint argument can be an explicit autosnap ref, a checkpoint commit hash,
or one of these current-branch history selectors:

  first
  first+N
  last
  last-N

Examples: first+1 selects the second checkpoint. last-1 selects the checkpoint
immediately before the latest checkpoint.`),
		Example: strings.TrimSpace(`autosnap unpick last
autosnap unpick last-1
autosnap unpick first+2
autosnap unpick first+2..last
autosnap unpick refs/autosnapshots/main/20260605T120000Z`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := context.Background()
			normalizedConflictPolicy, err := normalizeUnpickConflictPolicy(conflictPolicy)
			if err != nil {
				return err
			}

			repoRoot, _, branchRef, err := detectRepository(ctx)
			if err != nil {
				return err
			}

			patchRange, err := unpickCheckpointRange(ctx, repoRoot, branchRef, args[0], force, normalizedConflictPolicy)
			if err != nil {
				var conflictErr patchConflictError
				if errors.As(err, &conflictErr) {
					conflictErr.checkpoint = args[0]
					return conflictErr
				}
				return fmt.Errorf("failed to unpick checkpoint %q: %w", args[0], err)
			}

			if patchRange.Start.Ref == patchRange.End.Ref {
				fmt.Fprintf(out, "checkpoint unpicked: %s %s\n", path.Base(patchRange.End.Ref), patchRange.End.Commit)
			} else {
				fmt.Fprintf(out, "checkpoint range unpicked: %s..%s %s\n", path.Base(patchRange.Start.Ref), path.Base(patchRange.End.Ref), patchRange.End.Commit)
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Skip the clean worktree/index precheck")
	cmd.Flags().StringVar(&conflictPolicy, "conflict", conflictPolicyManual, "Conflict resolution policy: manual, base, head")
	return cmd
}
