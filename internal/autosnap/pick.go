package autosnap

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/spf13/cobra"
)

func newPickCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "pick <checkpoint>",
		Short: "Apply a checkpoint's incremental patch",
		Long: strings.TrimSpace(`Apply the same patch displayed by autosnap show <checkpoint>.

The checkpoint argument can be an explicit autosnap ref, a checkpoint commit hash,
or one of these current-branch history selectors:

  first
  first+N
  last
  last-N

Examples: first+1 selects the second checkpoint. last-1 selects the checkpoint
immediately before the latest checkpoint.`),
		Example: strings.TrimSpace(`autosnap pick last
autosnap pick last-1
autosnap pick first+2
autosnap pick refs/autosnapshots/main/20260605T120000Z`),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := context.Background()
			repoRoot, _, branchRef, err := detectRepository(ctx)
			if err != nil {
				return err
			}

			meta, err := resolveShowCheckpointRefMetadata(ctx, repoRoot, branchRef, args[0])
			if err != nil {
				return err
			}

			if err := pickCheckpoint(ctx, repoRoot, branchRef, meta, force); err != nil {
				var conflictErr patchConflictError
				if errors.As(err, &conflictErr) {
					conflictErr.checkpoint = args[0]
					return conflictErr
				}
				return fmt.Errorf("failed to pick checkpoint %q: %w", args[0], err)
			}

			fmt.Fprintf(out, "checkpoint picked: %s %s\n", path.Base(meta.Ref), meta.Commit)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Skip the clean worktree/index precheck")
	return cmd
}
