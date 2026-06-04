package autosnap

import (
	"context"
	"fmt"
	"path"

	"github.com/spf13/cobra"
)

func newRestoreCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "restore <checkpoint>",
		Short: "Restore checkpoint changes into the worktree",
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

			if err := restoreCheckpoint(ctx, repoRoot, meta, force); err != nil {
				return fmt.Errorf("failed to restore checkpoint %q: %w", args[0], err)
			}

			fmt.Fprintf(out, "checkpoint restored: %s %s\n", path.Base(meta.Ref), meta.Commit)
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Skip the clean worktree/index precheck")
	return cmd
}
