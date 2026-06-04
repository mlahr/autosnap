package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func newStartCommand() *cobra.Command {
	var (
		checkCommand string
		idleSeconds  int
	)

	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start autosnap watcher and checkpoint on idle passing checks",
		RunE: func(cmd *cobra.Command, args []string) error {
			if checkCommand == "" {
				return errors.New("--check is required")
			}
			if idleSeconds <= 0 {
				return errors.New("--idle must be greater than 0")
			}

			repoRoot, branchDisplay, branchRef, err := detectRepository(context.Background())
			if err != nil {
				return err
			}

			ctx, cancel := context.WithCancel(context.Background())
			waitUntilSignal(cancel)

			statePath, err := stateFilePath(repoRoot)
			if err != nil {
				return err
			}

			runner, err := newSnapshotRunner(ctx, repoRoot, branchRef, checkCommand, time.Duration(idleSeconds)*time.Second, statePath)
			if err != nil {
				return err
			}

			fmt.Printf("autosnap watching %s\n", repoRoot)
			fmt.Printf("branch: %s check: %s idle: %ds\n", branchDisplay, checkCommand, idleSeconds)

			if err := runner.start(); err != nil {
				cancel()
				return err
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&checkCommand, "check", "", "Shell command to run after idle")
	cmd.Flags().IntVar(&idleSeconds, "idle", 60, "Seconds without changes before running the check")

	return cmd
}
