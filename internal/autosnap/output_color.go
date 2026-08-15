package autosnap

const (
	ansiReset      = "\x1b[0m"
	ansiRed        = "\x1b[31m"
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

func colorizeCheckpointMark(enabled bool, value, state string) string {
	switch state {
	case checkpointMarkStateBad:
		return colorize(enabled, ansiRed, value)
	case checkpointMarkStateGood:
		return colorize(enabled, ansiGreen, value)
	case checkpointMarkStateReview:
		return colorize(enabled, ansiYellow, value)
	default:
		return colorize(enabled, ansiYellow, value)
	}
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

func colorizePatchStatus(enabled bool, status checkpointPatchStatus) string {
	if !enabled {
		return string(status)
	}

	switch status {
	case checkpointPatchStatusIncluded:
		return colorize(true, ansiGreen, string(status))
	case checkpointPatchStatusMissing:
		return colorize(true, ansiCyan, string(status))
	case checkpointPatchStatusConflict:
		return colorize(true, ansiYellow, string(status))
	default:
		return string(status)
	}
}
