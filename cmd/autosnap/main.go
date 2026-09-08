package main

import (
	"fmt"
	"os"

	autosnap "autosnap/internal/autosnap"
	"github.com/spf13/cobra"
)

var version = "dev"

func main() {
	root := newRootCommand(version)

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newRootCommand(version string) *cobra.Command {
	root := autosnap.NewRootCommand()
	root.Version = version
	root.SetVersionTemplate("autosnap {{.Version}}\n")
	return root
}
