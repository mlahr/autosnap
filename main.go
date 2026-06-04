package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
)

func main() {
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

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
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
