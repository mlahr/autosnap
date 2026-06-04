package main

import (
	"os"

	autosnap "autosnap/internal/autosnap"
)

var version = "dev"

func main() {
	root := autosnap.NewRootCommand()
	root.Version = version
	root.SetVersionTemplate("autosnap {{.Version}}\n")

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}
