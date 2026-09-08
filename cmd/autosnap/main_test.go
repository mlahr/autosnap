package main

import (
	"bytes"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	root := newRootCommand("test")
	root.SetArgs([]string{"--version"})
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetErr(&output)

	if err := root.Execute(); err != nil {
		t.Fatalf("execute --version failed: %v", err)
	}
	if got, want := output.String(), "autosnap test\n"; got != want {
		t.Fatalf("version output = %q, want %q", got, want)
	}
}
