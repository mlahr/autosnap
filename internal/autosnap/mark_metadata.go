package autosnap

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const checkpointMarkNoteRef = "refs/notes/autosnap/mark"

const (
	checkpointMarkStateUnmarked = "unmarked"
	checkpointMarkStateReview   = "review"
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

func markCheckpointReview(ctx context.Context, repoRoot string, checkpoint checkpointRefInfo, now time.Time) error {
	return writeCheckpointMark(ctx, repoRoot, checkpoint, checkpointMarkStateReview, "", now)
}

func validateCheckpointMarkLabel(label string) (string, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return "", fmt.Errorf("mark label cannot be empty")
	}
	if label == checkpointMarkStateUnmarked {
		return "", fmt.Errorf("mark label %q is reserved", label)
	}
	if len(label) > 32 {
		return "", fmt.Errorf("mark label %q exceeds the 32-character limit", label)
	}
	for i, r := range label {
		valid := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if i > 0 {
			valid = valid || r == '_' || r == '-'
		}
		if !valid || (i == 0 && (r == '_' || r == '-')) {
			return "", fmt.Errorf("invalid mark label %q: use 1-32 characters matching [A-Za-z0-9][A-Za-z0-9_-]*", label)
		}
	}
	return label, nil
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
	if len(checkpoints) == 0 {
		return marks, nil
	}

	fullCommitByRef, err := checkpointFullCommits(ctx, repoRoot, checkpoints)
	if err != nil {
		return nil, err
	}

	blobByCommit, err := listCheckpointMarkBlobIDs(ctx, repoRoot)
	if err != nil {
		return nil, err
	}

	blobIDs := make([]string, 0, len(checkpoints))
	seenBlob := map[string]struct{}{}
	for _, checkpoint := range checkpoints {
		marks[checkpoint.Ref] = checkpointMark{Mark: checkpointMarkStateUnmarked}
		fullCommit := fullCommitByRef[checkpoint.Ref]
		blobID := blobByCommit[fullCommit]
		if blobID == "" {
			continue
		}
		if _, ok := seenBlob[blobID]; ok {
			continue
		}
		seenBlob[blobID] = struct{}{}
		blobIDs = append(blobIDs, blobID)
	}
	if len(blobIDs) == 0 {
		return marks, nil
	}

	blobContents, err := readGitBlobsBatch(ctx, repoRoot, blobIDs)
	if err != nil {
		return nil, err
	}

	markByBlob := map[string]checkpointMark{}
	for _, checkpoint := range checkpoints {
		fullCommit := fullCommitByRef[checkpoint.Ref]
		blobID := blobByCommit[fullCommit]
		if blobID == "" {
			continue
		}
		mark, ok := markByBlob[blobID]
		if !ok {
			raw, ok := blobContents[blobID]
			if !ok {
				return nil, fmt.Errorf("failed to read checkpoint mark blob %s for %s", blobID, checkpoint.Ref)
			}
			var parseErr error
			mark, parseErr = parseCheckpointMark(checkpoint.Ref, raw)
			if parseErr != nil {
				return nil, parseErr
			}
			markByBlob[blobID] = mark
		}
		marks[checkpoint.Ref] = mark
	}
	return marks, nil
}

func checkpointFullCommits(ctx context.Context, repoRoot string, checkpoints []checkpointInfo) (map[string]string, error) {
	fullCommitByRef := map[string]string{}
	missing := make([]checkpointInfo, 0)
	for _, checkpoint := range checkpoints {
		fullCommit := strings.TrimSpace(checkpoint.FullCommit)
		if fullCommit == "" && looksLikeObjectID(checkpoint.Commit) && len(strings.TrimSpace(checkpoint.Commit)) == 40 {
			fullCommit = strings.TrimSpace(checkpoint.Commit)
		}
		if fullCommit != "" {
			fullCommitByRef[checkpoint.Ref] = fullCommit
			continue
		}
		missing = append(missing, checkpoint)
	}
	if len(missing) == 0 {
		return fullCommitByRef, nil
	}

	args := make([]string, 0, len(missing)+1)
	args = append(args, "rev-parse")
	for _, checkpoint := range missing {
		args = append(args, checkpoint.Ref)
	}
	result, err := runGitCommand(ctx, repoRoot, nil, args...)
	if err != nil {
		return nil, gitCommandError(err, result)
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) != len(missing) {
		return nil, fmt.Errorf("checkpoint full commit batch count mismatch: expected %d, got %d", len(missing), len(lines))
	}
	for i, line := range lines {
		fullCommit := strings.TrimSpace(line)
		if fullCommit == "" {
			return nil, fmt.Errorf("empty full commit for checkpoint %s", missing[i].Ref)
		}
		fullCommitByRef[missing[i].Ref] = fullCommit
	}
	return fullCommitByRef, nil
}

func listCheckpointMarkBlobIDs(ctx context.Context, repoRoot string) (map[string]string, error) {
	blobByCommit := map[string]string{}
	result, err := runGitCommand(ctx, repoRoot, nil, "notes", "--ref", checkpointMarkNoteRef, "list")
	if err != nil {
		return nil, gitCommandError(err, result)
	}
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		parts := strings.Fields(trimmed)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid checkpoint mark note list line %q", line)
		}
		blobByCommit[parts[1]] = parts[0]
	}
	return blobByCommit, nil
}

func readGitBlobsBatch(ctx context.Context, repoRoot string, blobIDs []string) (map[string]string, error) {
	if len(blobIDs) == 0 {
		return nil, nil
	}

	input := strings.Join(blobIDs, "\n") + "\n"
	result, err := runGitCommandWithInput(ctx, repoRoot, nil, input, "cat-file", "--batch")
	if err != nil {
		return nil, gitCommandError(err, result)
	}
	return parseGitCatFileBatch(result.Stdout)
}

func parseGitCatFileBatch(output string) (map[string]string, error) {
	blobs := map[string]string{}
	remaining := output
	for strings.TrimSpace(remaining) != "" {
		headerEnd := strings.IndexByte(remaining, '\n')
		if headerEnd < 0 {
			return nil, fmt.Errorf("invalid git cat-file batch output: missing header newline")
		}
		header := remaining[:headerEnd]
		parts := strings.Fields(header)
		if len(parts) == 2 && parts[1] == "missing" {
			return nil, fmt.Errorf("git object %s is missing", parts[0])
		}
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid git cat-file batch header %q", header)
		}
		size, err := strconv.Atoi(parts[2])
		if err != nil {
			return nil, fmt.Errorf("invalid git cat-file batch size %q: %w", parts[2], err)
		}
		contentStart := headerEnd + 1
		contentEnd := contentStart + size
		if len(remaining) < contentEnd {
			return nil, fmt.Errorf("truncated git cat-file batch object %s", parts[0])
		}
		blobs[parts[0]] = remaining[contentStart:contentEnd]
		remaining = remaining[contentEnd:]
		if strings.HasPrefix(remaining, "\n") {
			remaining = remaining[1:]
		}
	}
	return blobs, nil
}

func readCheckpointMark(ctx context.Context, repoRoot, checkpointRef string) (checkpointMark, error) {
	result, err := runGitCommand(ctx, repoRoot, nil, "notes", "--ref", checkpointMarkNoteRef, "show", checkpointRef)
	if err != nil {
		if isMissingGitNote(result) {
			return checkpointMark{Mark: checkpointMarkStateUnmarked}, nil
		}
		return checkpointMark{}, gitCommandError(err, result)
	}

	return parseCheckpointMark(checkpointRef, result.Stdout)
}

func parseCheckpointMark(checkpointRef, raw string) (checkpointMark, error) {
	var mark checkpointMark
	if err := json.Unmarshal([]byte(raw), &mark); err != nil {
		return checkpointMark{}, fmt.Errorf("invalid checkpoint mark for %s in %s: %w", checkpointRef, checkpointMarkNoteRef, err)
	}
	if _, err := validateCheckpointMarkLabel(mark.Mark); err != nil {
		return checkpointMark{}, fmt.Errorf("invalid checkpoint mark state %q for %s in %s: %w", mark.Mark, checkpointRef, checkpointMarkNoteRef, err)
	}
	return mark, nil
}
