package main

import (
	"os"

	autosnap "autosnap/internal/autosnap"
)

func main() {
	if err := autosnap.NewRootCommand().Execute(); err != nil {
		os.Exit(1)
	}
}
