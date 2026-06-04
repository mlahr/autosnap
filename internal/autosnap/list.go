package autosnap

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List checkpoints for the current branch",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			repoRoot, _, branchRef, err := detectRepository(ctx)
			if err != nil {
				return err
			}

			checkpoints, err := listCheckpointsForBranch(ctx, repoRoot, branchRef)
			if err != nil {
				return err
			}
			if len(checkpoints) == 0 {
				fmt.Println("no checkpoints for current branch")
				return nil
			}

			for _, cp := range checkpoints {
				fmt.Printf("%s %s %s\n", cp.Timestamp, cp.Commit, cp.Summary)
			}

			return nil
		},
	}
}
