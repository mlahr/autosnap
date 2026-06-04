package autosnap

import (
	"context"
	"fmt"
	"path"

	"github.com/spf13/cobra"
)

func newPromoteCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "promote <checkpoint>",
		Short: "Promote a checkpoint to a normal branch commit",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := context.Background()
			repoRoot, _, branchRef, err := detectRepository(ctx)
			if err != nil {
				return err
			}

			meta, err := resolveCheckpointRefMetadata(ctx, repoRoot, branchRef, args[0])
			if err != nil {
				return err
			}

			commit, created, err := promoteCheckpoint(ctx, repoRoot, meta, force)
			if err != nil {
				return fmt.Errorf("failed to promote checkpoint %q: %w", args[0], err)
			}
			if !created {
				fmt.Fprintf(out, "checkpoint already matches HEAD: %s %s\n", path.Base(meta.Ref), commit)
				return nil
			}

			fmt.Fprintf(out, "checkpoint promoted: %s %s\n", path.Base(meta.Ref), commit)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Skip the clean worktree/index precheck")
	return cmd
}
