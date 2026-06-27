package autosnap

import (
	"context"
	"strings"
)

func filterPendingCheckpointRefs(ctx context.Context, repoRoot string, checkpoints []checkpointRefInfo, branch string, allBranches bool) ([]checkpointRefInfo, error) {
	return actionablePendingCheckpointRefsDebug(ctx, repoRoot, checkpoints, branch, allBranches, pendingDebugLogger{})
}

func shortObjectID(objectID string) string {
	if len(objectID) <= 12 {
		return objectID
	}
	return objectID[:12]
}

func isMergeTreeWriteTreeUnsupported(result commandResult) bool {
	combined := strings.ToLower(result.Stdout + "\n" + result.Stderr)
	return result.ExitCode == 129 ||
		strings.Contains(combined, "unknown option") ||
		strings.Contains(combined, "usage: git merge-tree")
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func looksLikeObjectID(candidate string) bool {
	if len(candidate) != 40 {
		return false
	}
	for _, ch := range candidate {
		if (ch >= '0' && ch <= '9') ||
			(ch >= 'a' && ch <= 'f') ||
			(ch >= 'A' && ch <= 'F') {
			continue
		}
		return false
	}
	return true
}
