package autosnap

import (
	"strings"
)

const (
	ansiReset      = "\x1b[0m"
	ansiGreen      = "\x1b[32m"
	ansiYellow     = "\x1b[33m"
	ansiBlue       = "\x1b[34m"
	ansiMagenta    = "\x1b[35m"
	ansiCyan       = "\x1b[36m"
	ansiGhostWhite = "\x1b[38;2;248;248;255m"
)

func colorize(enabled bool, code, value string) string {
	if !enabled || value == "" {
		return value
	}

	return code + value + ansiReset
}

func colorizeWorktreeMatchMarker(enabled bool, match checkpointWorktreeMatch) string {
	switch {
	case !enabled:
		return checkpointWorktreeMatchMarker(match)
	case match == checkpointWorktreeMatchWorktree:
		return colorize(true, ansiGreen, checkpointWorktreeMatchMarker(match))
	case match == checkpointWorktreeMatchIndex:
		return colorize(true, ansiBlue, checkpointWorktreeMatchMarker(match))
	default:
		return checkpointWorktreeMatchMarker(match)
	}
}

func colorizeCheckpointID(enabled bool, value string) string {
	return colorize(enabled, ansiCyan, value)
}

func colorizeCommitMessage(enabled bool, value string) string {
	return colorize(enabled, ansiGhostWhite, value)
}

func colorizePendingStatus(enabled bool, status checkpointPendingStatus) string {
	if !enabled {
		return string(status)
	}

	switch status {
	case checkpointStatusExact:
		return colorize(true, ansiGreen, string(status))
	case checkpointStatusIntegrated:
		return colorize(true, ansiBlue, string(status))
	case checkpointStatusObsolete:
		return colorize(true, ansiMagenta, string(status))
	case checkpointStatusConflict:
		return colorize(true, ansiYellow, string(status))
	case checkpointStatusPending:
		return colorize(true, ansiCyan, string(status))
	default:
		return string(status)
	}
}

func colorizePendingStatusPadded(enabled bool, status checkpointPendingStatus, width int) string {
	raw := string(status)
	padding := 0
	if len(raw) < width {
		padding = width - len(raw)
	}

	statusText := colorizePendingStatus(enabled, status)
	if statusText == raw {
		return raw + strings.Repeat(" ", padding)
	}

	return statusText + strings.Repeat(" ", padding)
}
