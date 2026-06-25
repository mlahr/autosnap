package main

import (
	"fmt"
	"os"

	autosnap "autosnap/internal/autosnap"
)

var version = "dev"

func main() {
	root := autosnap.NewRootCommand()
	root.Version = version
	root.SetVersionTemplate("autosnap {{.Version}}\n")

	if err := root.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
