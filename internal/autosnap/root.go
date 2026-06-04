package autosnap

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
)

func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "autosnap",
		Short: "Local checkpointing for Git worktrees",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	root.AddCommand(newStartCommand())
	root.AddCommand(newStatusCommand())
	root.AddCommand(newListCommand())
	root.AddCommand(newShowCommand())
	root.AddCommand(newRestoreCommand())
	root.AddCommand(newPromoteCommand())
	root.AddCommand(newPruneCommand())
	root.AddCommand(newStopCommand())
	root.AddCommand(newConfigCommand())

	return root
}

func waitUntilSignal(stop context.CancelFunc) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	go func() {
		<-sigCh
		fmt.Println("\nstopping autosnap")
		stop()
	}()
}
