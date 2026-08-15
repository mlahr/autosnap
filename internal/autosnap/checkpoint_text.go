package autosnap

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

const (
	checkpointTimestampColumnWidth = 23
	checkpointStatusColumnWidth    = 10
	checkpointPatchColumnWidth     = 8
	checkpointCommitColumnWidth    = 9
	checkpointMatchColumnWidth     = 2
	checkpointMarkColumnWidth      = 8
)

type checkpointTextRow struct {
	Branch      string
	Timestamp   string
	Status      checkpointPendingStatus
	PatchStatus checkpointPatchStatus
	Commit      string
	Match       checkpointWorktreeMatch
	Mark        checkpointMark
	Summary     string
}

type checkpointTextColumns struct {
	BranchWidth        int
	CommitWidth        int
	MarkWidth          int
	TimestampWidth     int
	IncludeStatus      bool
	IncludePatchStatus bool
}

func checkpointTextBranchWidth(checkpoints []checkpointInfo, allBranches bool) int {
	if !allBranches {
		return 0
	}
	width := 0
	for _, checkpoint := range checkpoints {
		if branchWidth := runewidth.StringWidth(checkpoint.Branch); branchWidth > width {
			width = branchWidth
		}
	}
	return width
}

func checkpointTextTimestampWidth(checkpoints []checkpointInfo) int {
	timestamps := make([]string, 0, len(checkpoints))
	for _, checkpoint := range checkpoints {
		timestamps = append(timestamps, formatCheckpointTimestampForList(checkpoint.Timestamp))
	}
	return checkpointTextTimestampWidthForValues(timestamps)
}

func checkpointTextTimestampWidthForValues(timestamps []string) int {
	width := checkpointTimestampColumnWidth
	for _, timestamp := range timestamps {
		timestampWidth := runewidth.StringWidth(timestamp)
		if timestampWidth > width {
			width = timestampWidth
		}
	}
	return width
}

func checkpointTextMarkWidth(checkpoints []checkpointInfo, marks map[string]checkpointMark) int {
	width := checkpointMarkColumnWidth
	for _, checkpoint := range checkpoints {
		if markWidth := len(checkpointMarkDisplay(marks[checkpoint.Ref])); markWidth > width {
			width = markWidth
		}
	}
	return width
}

func checkpointTextCommitWidth(checkpoints []checkpointInfo) int {
	width := checkpointCommitColumnWidth
	for _, checkpoint := range checkpoints {
		if commitWidth := runewidth.StringWidth(checkpoint.Commit); commitWidth > width {
			width = commitWidth
		}
	}
	return width
}

func formatCheckpointTextRow(row checkpointTextRow, columns checkpointTextColumns, useColor bool) string {
	fields := make([]string, 0, 7)
	markWidth := columns.MarkWidth
	if markWidth == 0 {
		markWidth = checkpointMarkColumnWidth
	}
	commitWidth := columns.CommitWidth
	if commitWidth == 0 {
		commitWidth = checkpointCommitColumnWidth
	}
	timestampWidth := columns.TimestampWidth
	if timestampWidth == 0 {
		timestampWidth = checkpointTimestampColumnWidth
	}
	if columns.BranchWidth > 0 {
		fields = append(fields, fixedCheckpointTextColumn(row.Branch, row.Branch, columns.BranchWidth))
	}
	fields = append(fields, fixedCheckpointTextColumn(row.Timestamp, row.Timestamp, timestampWidth))
	if columns.IncludeStatus {
		status := string(row.Status)
		fields = append(fields, fixedCheckpointTextColumn(status, colorizePendingStatus(useColor, row.Status), checkpointStatusColumnWidth))
	}
	if columns.IncludePatchStatus {
		patchStatus := row.PatchStatus
		if patchStatus == "" {
			patchStatus = checkpointPatchStatusConflict
		}
		fields = append(fields, fixedCheckpointTextColumn(string(patchStatus), colorizePatchStatus(useColor, patchStatus), checkpointPatchColumnWidth))
	}
	fields = append(fields,
		fixedCheckpointTextColumn(row.Commit, colorizeCheckpointID(useColor, row.Commit), commitWidth),
		fixedCheckpointTextColumn(checkpointWorktreeMatchMarker(row.Match), colorizeWorktreeMatchMarker(useColor, row.Match), checkpointMatchColumnWidth),
		formatCheckpointMarkColumn(row.Mark, useColor, markWidth),
	)
	return strings.Join(fields, " ") + " " + colorizeCommitMessage(useColor, row.Summary)
}

func fixedCheckpointTextColumn(raw, styled string, width int) string {
	if padding := width - runewidth.StringWidth(raw); padding > 0 {
		return styled + strings.Repeat(" ", padding)
	}
	return styled
}

func formatCheckpointMarkColumn(mark checkpointMark, useColor bool, width int) string {
	raw := checkpointMarkDisplay(mark)
	styled := raw
	switch mark.Mark {
	case checkpointMarkStateBad, checkpointMarkStateGood, checkpointMarkStateReview:
		styled = colorizeCheckpointMark(useColor, raw, mark.Mark)
	}
	return fixedCheckpointTextColumn(raw, styled, width)
}

func checkpointMarkDisplay(mark checkpointMark) string {
	label := strings.TrimSpace(mark.Mark)
	if label == "" || label == checkpointMarkStateUnmarked {
		return ""
	}
	return label
}
