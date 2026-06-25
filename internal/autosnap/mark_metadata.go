package autosnap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const checkpointMarkNoteRef = "refs/notes/autosnap/mark"

const (
	checkpointMarkStateUnmarked = "unmarked"
	checkpointMarkStateGood     = "good"
	checkpointMarkStateBad      = "bad"
)

type checkpointMark struct {
	Mark     string `json:"mark"`
	Reason   string `json:"reason,omitempty"`
	MarkedAt string `json:"markedAt,omitempty"`
}

func markCheckpointBad(ctx context.Context, repoRoot string, checkpoint checkpointRefInfo, reason string, now time.Time) error {
	return writeCheckpointMark(ctx, repoRoot, checkpoint, checkpointMarkStateBad, reason, now)
}

func markCheckpointGood(ctx context.Context, repoRoot string, checkpoint checkpointRefInfo, now time.Time) error {
	return writeCheckpointMark(ctx, repoRoot, checkpoint, checkpointMarkStateGood, "", now)
}

func writeCheckpointMark(ctx context.Context, repoRoot string, checkpoint checkpointRefInfo, state, reason string, now time.Time) error {
	payload := checkpointMark{
		Mark:     state,
		Reason:   strings.TrimSpace(reason),
		MarkedAt: now.UTC().Format(time.RFC3339),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	result, err := runGitCommand(ctx, repoRoot, nil, "notes", "--ref", checkpointMarkNoteRef, "add", "-f", "-m", string(raw), checkpointRefCommit(checkpoint))
	if err != nil {
		return gitCommandError(err, result)
	}
	return nil
}

func unmarkCheckpoint(ctx context.Context, repoRoot string, checkpoint checkpointRefInfo) error {
	result, err := runGitCommand(ctx, repoRoot, nil, "notes", "--ref", checkpointMarkNoteRef, "remove", checkpointRefCommit(checkpoint))
	if err != nil {
		if isMissingGitNote(result) {
			return nil
		}
		return gitCommandError(err, result)
	}
	return nil
}

func checkpointMarks(ctx context.Context, repoRoot string, checkpoints []checkpointInfo) (map[string]checkpointMark, error) {
	marks := map[string]checkpointMark{}
	for _, checkpoint := range checkpoints {
		mark, err := readCheckpointMark(ctx, repoRoot, checkpoint.Ref)
		if err != nil {
			return nil, err
		}
		marks[checkpoint.Ref] = mark
	}
	return marks, nil
}

func readCheckpointMark(ctx context.Context, repoRoot, checkpointRef string) (checkpointMark, error) {
	result, err := runGitCommand(ctx, repoRoot, nil, "notes", "--ref", checkpointMarkNoteRef, "show", checkpointRef)
	if err != nil {
		if isMissingGitNote(result) {
			return checkpointMark{Mark: checkpointMarkStateUnmarked}, nil
		}
		return checkpointMark{}, gitCommandError(err, result)
	}

	var mark checkpointMark
	if err := json.Unmarshal([]byte(result.Stdout), &mark); err != nil {
		return checkpointMark{}, fmt.Errorf("invalid checkpoint mark for %s in %s: %w", checkpointRef, checkpointMarkNoteRef, err)
	}
	switch mark.Mark {
	case checkpointMarkStateGood, checkpointMarkStateBad:
		return mark, nil
	default:
		return checkpointMark{}, fmt.Errorf("invalid checkpoint mark state %q for %s in %s", mark.Mark, checkpointRef, checkpointMarkNoteRef)
	}
}

func checkpointMarkSummaryPrefix(useColor bool, mark checkpointMark) string {
	switch mark.Mark {
	case checkpointMarkStateGood:
		return colorizeCheckpointMark(useColor, "[good]", checkpointMarkStateGood)
	case checkpointMarkStateBad:
		return colorizeCheckpointMark(useColor, "[bad]", checkpointMarkStateBad)
	default:
		return ""
	}
}

func checkpointMarkSummary(useColor bool, mark checkpointMark, summary string) string {
	prefix := checkpointMarkSummaryPrefix(useColor, mark)
	if prefix == "" {
		return colorizeCommitMessage(useColor, summary)
	}
	return prefix + " " + colorizeCommitMessage(useColor, summary)
}
