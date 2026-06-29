package autosnap

import "testing"

func TestColorizeCommitMessage(t *testing.T) {
	t.Parallel()

	in := "commit summary"
	out := colorizeCommitMessage(true, in)
	if want := "\x1b[38;2;248;248;255m" + in + "\x1b[0m"; out != want {
		t.Fatalf("expected ghostwhite commit message color, want %q, got %q", want, out)
	}
}

func TestColorizeCheckpointID(t *testing.T) {
	t.Parallel()

	in := "abc123"
	out := colorizeCheckpointID(true, in)
	if want := "\x1b[36m" + in + "\x1b[0m"; out != want {
		t.Fatalf("expected aqua checkpoint id color, want %q, got %q", want, out)
	}
}

func TestColorizeWorktreeMatchMarker(t *testing.T) {
	t.Parallel()

	in := checkpointWorktreeMatchWorktree
	out := colorizeWorktreeMatchMarker(true, in)
	if out != "\x1b[32m**\x1b[0m" {
		t.Fatalf("expected green worktree marker, got %q", out)
	}

	in = checkpointWorktreeMatchIndex
	out = colorizeWorktreeMatchMarker(true, in)
	if out != "\x1b[34m* \x1b[0m" {
		t.Fatalf("expected blue index marker, got %q", out)
	}
}

func TestColorizePatchStatusPadded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status checkpointPatchStatus
		want   string
	}{
		{
			name:   "included",
			status: checkpointPatchStatusIncluded,
			want:   "\x1b[32mincluded\x1b[0m",
		},
		{
			name:   "missing",
			status: checkpointPatchStatusMissing,
			want:   "\x1b[36mmissing\x1b[0m ",
		},
		{
			name:   "conflict",
			status: checkpointPatchStatusConflict,
			want:   "\x1b[33mconflict\x1b[0m",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := colorizePatchStatusPadded(true, tt.status, 8); got != tt.want {
				t.Fatalf("expected patch status color %q, got %q", tt.want, got)
			}
		})
	}
}
