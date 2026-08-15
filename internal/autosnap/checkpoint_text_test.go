package autosnap

import (
	"strings"
	"testing"
)

func TestFormatCheckpointTextRowUsesFixedColumns(t *testing.T) {
	row := checkpointTextRow{
		Timestamp:   "2026-08-15 18:48:00 HKT",
		Status:      checkpointStatusIntegrated,
		PatchStatus: checkpointPatchStatusIncluded,
		Commit:      "7db6dd2c7",
		Mark:        checkpointMark{Mark: checkpointMarkStateGood},
		Summary:     "summary",
	}
	got := formatCheckpointTextRow(row, checkpointTextColumns{IncludeStatus: true, IncludePatchStatus: true}, false)
	want := "2026-08-15 18:48:00 HKT integrated included 7db6dd2c7    good     summary"
	if got != want {
		t.Fatalf("unexpected row\nwant: %q\n got: %q", want, got)
	}
}

func TestFormatCheckpointTextRowAlignsObsoleteAndIntegrated(t *testing.T) {
	base := checkpointTextRow{
		Timestamp: "2026-08-15 18:48:00 HKT",
		Commit:    "7db6dd2c7",
		Summary:   "summary",
	}
	obsolete := base
	obsolete.Status = checkpointStatusObsolete
	obsolete.PatchStatus = checkpointPatchStatusConflict
	integrated := base
	integrated.Status = checkpointStatusIntegrated
	integrated.PatchStatus = checkpointPatchStatusIncluded
	columns := checkpointTextColumns{IncludeStatus: true, IncludePatchStatus: true}
	obsoleteText := formatCheckpointTextRow(obsolete, columns, false)
	integratedText := formatCheckpointTextRow(integrated, columns, false)
	if got, want := strings.Index(obsoleteText, "conflict"), strings.Index(integratedText, "included"); got != want {
		t.Fatalf("patch-status columns differ: obsolete=%d integrated=%d", got, want)
	}
	if got, want := strings.Index(obsoleteText, base.Commit), strings.Index(integratedText, base.Commit); got != want {
		t.Fatalf("commit columns differ: obsolete=%d integrated=%d", got, want)
	}
}

func TestFormatCheckpointTextRowPreservesLongMark(t *testing.T) {
	row := checkpointTextRow{
		Timestamp: "2026-08-15 18:48:00 HKT",
		Commit:    "7db6dd2c7",
		Mark:      checkpointMark{Mark: "caching-fix"},
		Summary:   "summary",
	}
	got := formatCheckpointTextRow(row, checkpointTextColumns{}, false)
	want := "2026-08-15 18:48:00 HKT 7db6dd2c7    caching-fix summary"
	if got != want {
		t.Fatalf("unexpected row\nwant: %q\n got: %q", want, got)
	}
}

func TestCheckpointTextMarkWidthPreservesFullLabels(t *testing.T) {
	checkpoints := []checkpointInfo{{Ref: "short"}, {Ref: "long"}}
	m := map[string]checkpointMark{
		"short": {Mark: "good"},
		"long":  {Mark: "caching-fix"},
	}
	if got := checkpointTextMarkWidth(checkpoints, m); got != len("caching-fix") {
		t.Fatalf("expected mark width %d, got %d", len("caching-fix"), got)
	}
}

func TestCheckpointTextCommitWidthPreservesAlignment(t *testing.T) {
	checkpoints := []checkpointInfo{
		{Commit: "abc123456"},
		{Commit: "abcdef1234567"},
	}
	if got := checkpointTextCommitWidth(checkpoints); got != len("abcdef1234567") {
		t.Fatalf("expected commit width %d, got %d", len("abcdef1234567"), got)
	}

	short := formatCheckpointTextRow(checkpointTextRow{Timestamp: "2026-08-15 18:48:00 HKT", Commit: "abc123456", Summary: "short"}, checkpointTextColumns{CommitWidth: 13}, false)
	long := formatCheckpointTextRow(checkpointTextRow{Timestamp: "2026-08-15 18:48:00 HKT", Commit: "abcdef1234567", Summary: "long"}, checkpointTextColumns{CommitWidth: 13}, false)
	if got, want := strings.Index(short, "short"), strings.Index(long, "long"); got != want {
		t.Fatalf("summary columns differ: short=%d long=%d", got, want)
	}
}

func TestCheckpointTextBranchWidth(t *testing.T) {
	checkpoints := []checkpointInfo{{Branch: "main"}, {Branch: "feature/長"}}
	if got := checkpointTextBranchWidth(checkpoints, true); got != 10 {
		t.Fatalf("expected longest branch width, got %d", got)
	}
	if got := checkpointTextBranchWidth(checkpoints, false); got != 0 {
		t.Fatalf("expected no branch column, got %d", got)
	}
}

func TestCheckpointTextTimestampWidthPreservesAlignment(t *testing.T) {
	width := checkpointTextTimestampWidthForValues([]string{
		"2026-01-15 12:00:00 CET",
		"2026-07-15 12:00:00 CEST",
	})
	if width < checkpointTimestampColumnWidth {
		t.Fatalf("expected timestamp width at least %d, got %d", checkpointTimestampColumnWidth, width)
	}

	short := formatCheckpointTextRow(checkpointTextRow{Timestamp: "2026-08-15 11:18:30 CET", Commit: "abc123456", Summary: "short"}, checkpointTextColumns{TimestampWidth: width}, false)
	long := formatCheckpointTextRow(checkpointTextRow{Timestamp: "2026-08-15 11:18:30 CEST", Commit: "abc123456", Summary: "long"}, checkpointTextColumns{TimestampWidth: width}, false)
	if got, want := strings.Index(short, "abc123456"), strings.Index(long, "abc123456"); got != want {
		t.Fatalf("commit columns differ: short=%d long=%d", got, want)
	}
}

func TestFormatCheckpointTextRowPadsUnicodeBranchByDisplayWidth(t *testing.T) {
	row := checkpointTextRow{
		Branch:    "長",
		Timestamp: "2026-08-15 18:48:00 HKT",
		Commit:    "7db6dd2c7",
		Summary:   "summary",
	}
	got := formatCheckpointTextRow(row, checkpointTextColumns{BranchWidth: 4}, false)
	want := "長   2026-08-15 18:48:00 HKT 7db6dd2c7             summary"
	if got != want {
		t.Fatalf("unexpected Unicode branch row\nwant: %q\n got: %q", want, got)
	}
}
