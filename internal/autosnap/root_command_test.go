package autosnap

import (
	"bytes"
	"path"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestSnapshotRefNaming(t *testing.T) {
	t.Parallel()
	prefix := snapshotRefPrefix("feature/foo")
	if prefix != path.Join("refs", "autosnapshots", "feature/foo") {
		t.Fatalf("unexpected prefix: %s", prefix)
	}

	ref := snapshotRef("feature/foo", "20260101T120000Z")
	if ref != path.Join("refs", "autosnapshots", "feature/foo", "20260101T120000Z") {
		t.Fatalf("unexpected ref: %s", ref)
	}
}

func TestParseCheckpointMessage(t *testing.T) {
	message := "autosnap: passing checkpoint 2026-06-04T13:22:10 branch: feature/foo check: npm test idle_seconds: 60 base: abc1234"

	status, cmd := parseCheckpointMessage(message)
	if status != "passing" {
		t.Fatalf("expected status passing, got %s", status)
	}
	if cmd != "npm test" {
		t.Fatalf("expected check command npm test, got %q", cmd)
	}

	status, cmd = parseCheckpointMessage("autosnap: failing checkpoint 2026-06-04T13:22:10")
	if status != "failing" {
		t.Fatalf("expected status failing, got %s", status)
	}
	if cmd != "" {
		t.Fatalf("expected empty check command, got %q", cmd)
	}

	status, cmd = parseCheckpointMessage("autosnap: check: test passing-check output branch: main idle_seconds: 10")
	if status != "unknown" {
		t.Fatalf("expected unknown when passing appears in check output, got %s", status)
	}
	if cmd != "test passing-check output" {
		t.Fatalf("expected parsed check command, got %q", cmd)
	}

	status, cmd = parseCheckpointMessage("feat(monitoring): introduce failing state for monitors")
	if status != "unknown" {
		t.Fatalf("expected unknown when failing appears in custom subject, got %s", status)
	}
	if cmd != "" {
		t.Fatalf("expected empty check command for custom subject, got %q", cmd)
	}
}

func TestCheckpointListSummary(t *testing.T) {
	tests := []struct {
		name    string
		message string
		want    string
	}{
		{
			name:    "generated message",
			message: "autosnap: passing checkpoint 2026-06-04T13:22:10 branch: feature/foo check: npm test idle_seconds: 60 base: abc1234",
			want:    "passing npm test",
		},
		{
			name:    "custom multiline message",
			message: "feat(autosnap): add command output logging\n\nbody line",
			want:    "feat(autosnap): add command output logging",
		},
		{
			name:    "custom subject containing status word",
			message: "feat(monitoring): introduce failing state for monitors",
			want:    "feat(monitoring): introduce failing state for monitors",
		},
		{
			name:    "empty message",
			message: "",
			want:    "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkpointListSummary(tt.message); got != tt.want {
				t.Fatalf("expected summary %q, got %q", tt.want, got)
			}
		})
	}
}

func TestRootCommandIncludesCheckpointCommand(t *testing.T) {
	root := NewRootCommand()
	for _, command := range root.Commands() {
		if command.Name() == "checkpoint" {
			return
		}
	}
	t.Fatalf("expected root command to include checkpoint")
}

func TestRootCommandIncludesBranchCommand(t *testing.T) {
	root := NewRootCommand()
	for _, command := range root.Commands() {
		if command.Name() == "branch" {
			return
		}
	}
	t.Fatalf("expected root command to include branch")
}

func TestRootCommandIncludesDocsCommand(t *testing.T) {
	root := NewRootCommand()
	for _, command := range root.Commands() {
		if command.Name() == "docs" {
			return
		}
	}
	t.Fatalf("expected root command to include docs")
}

func TestRootCommandIncludesMarkCommand(t *testing.T) {
	root := NewRootCommand()
	for _, command := range root.Commands() {
		if command.Name() == "mark" {
			return
		}
	}
	t.Fatalf("expected root command to include mark")
}

func TestMarkCommandValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing mode", args: []string{"mark", "last"}, want: "exactly one of --label, --bad, --good, --review, or --unmark"},
		{name: "multiple modes", args: []string{"mark", "--bad", "--review", "last"}, want: "exactly one of --label, --bad, --good, --review, or --unmark"},
		{name: "reason with good", args: []string{"mark", "--good", "--reason", "fixed", "last"}, want: "--reason can only be used with --label or --bad"},
		{name: "reason with review", args: []string{"mark", "--review", "--reason", "inspect", "last"}, want: "--reason can only be used with --label or --bad"},
		{name: "reason with unmark", args: []string{"mark", "--unmark", "--reason", "fixed", "last"}, want: "--reason can only be used with --label or --bad"},
		{name: "missing checkpoint argument", args: []string{"mark"}, want: "mark requires exactly one checkpoint-or-range argument"},
		{name: "too many checkpoint arguments", args: []string{"mark", "last", "first"}, want: "mark requires exactly one checkpoint-or-range argument"},
		{name: "invalid label", args: []string{"mark", "--label", "needs space", "last"}, want: "invalid mark label"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
			root.AddCommand(newMarkCommand())
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestBranchCopyCommandValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing from", args: []string{"branch", "copy", "--to", "feature/next"}, want: "branch copy requires --from"},
		{name: "missing to", args: []string{"branch", "copy", "--from", "main"}, want: "branch copy requires --to"},
		{name: "invalid from", args: []string{"branch", "copy", "--from", "bad..branch", "--to", "feature/next"}, want: "invalid --from"},
		{name: "invalid to", args: []string{"branch", "copy", "--from", "main", "--to", "bad..branch"}, want: "invalid --to"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
			root.AddCommand(newBranchCommand())
			root.SetArgs(tt.args)
			err := root.Execute()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestDocsCommandShowsInstalledDocumentationLocations(t *testing.T) {
	buf := &bytes.Buffer{}
	root := &cobra.Command{Use: "autosnap", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(newDocsCommand())
	root.SetOut(buf)
	root.SetArgs([]string{"docs"})

	if err := root.Execute(); err != nil {
		t.Fatalf("docs command failed: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"man autosnap",
		"man autosnap-<command>",
		"/usr/share/doc/autosnap/",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected docs output to contain %q, got %q", want, out)
		}
	}
}
