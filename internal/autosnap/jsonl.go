package autosnap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	outputFormatText  = "text"
	outputFormatJSONL = "jsonl"
)

type checkpointJSONLOptions struct {
	IncludeNotes  bool
	NoteRef       string
	PendingStatus map[string]checkpointPendingStatus
}

func normalizeOutputFormat(format string) (string, error) {
	switch strings.TrimSpace(format) {
	case "", outputFormatText:
		return outputFormatText, nil
	case outputFormatJSONL:
		return outputFormatJSONL, nil
	default:
		return "", fmt.Errorf("invalid --format value %q (expected text, jsonl)", format)
	}
}

func resolveOutputNoteRef(repoRoot, flagValue string) (string, error) {
	if strings.TrimSpace(flagValue) != "" {
		return strings.TrimSpace(flagValue), nil
	}

	cfg, _, err := loadAutosnapConfig(repoRoot)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(cfg.NoteRef) != "" {
		return strings.TrimSpace(cfg.NoteRef), nil
	}

	return "", fmt.Errorf("--notes-json requires --note-ref or configured note_ref")
}

func validateJSONLOutputOptions(format string, notesJSON bool) error {
	if notesJSON && format != outputFormatJSONL {
		return fmt.Errorf("--notes-json requires --format jsonl")
	}
	return nil
}

func writeCheckpointsJSONL(ctx context.Context, repoRoot string, out io.Writer, checkpoints []checkpointInfo, opts checkpointJSONLOptions) error {
	encoder := json.NewEncoder(out)
	for _, checkpoint := range checkpoints {
		row := map[string]any{
			"ref":       checkpoint.Ref,
			"commit":    checkpoint.Commit,
			"timestamp": formatCheckpointTimestampForList(checkpoint.Timestamp),
			"status":    checkpoint.Status,
			"check":     checkpoint.CheckCmd,
			"summary":   checkpoint.Summary,
		}
		if strings.TrimSpace(checkpoint.Branch) != "" {
			row["branch"] = checkpoint.Branch
		}
		if status, ok := opts.PendingStatus[checkpoint.Ref]; ok {
			row["pendingStatus"] = status
		}
		if opts.IncludeNotes {
			note, noteErr, err := readCheckpointJSONNote(ctx, repoRoot, opts.NoteRef, checkpoint.Ref)
			if err != nil {
				return err
			}
			row["noteRef"] = opts.NoteRef
			row["note"] = note
			if noteErr != "" {
				row["noteError"] = noteErr
			}
		}
		if err := encoder.Encode(row); err != nil {
			return err
		}
	}
	return nil
}

func readCheckpointJSONNote(ctx context.Context, repoRoot, noteRef, checkpointRef string) (any, string, error) {
	result, err := runGitCommand(ctx, repoRoot, nil, "notes", "--ref", noteRef, "show", checkpointRef)
	if err != nil {
		if isMissingGitNote(result) {
			return nil, "", nil
		}
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(result.Stdout)
		}
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Sprintf("failed to read note for %s in %s: %s", checkpointRef, noteRef, detail), nil
	}

	var payload any
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		return nil, fmt.Sprintf("invalid JSON note for %s in %s: %v", checkpointRef, noteRef, err), nil
	}
	return payload, "", nil
}

func isMissingGitNote(result commandResult) bool {
	if result.ExitCode == 0 {
		return false
	}
	detail := strings.ToLower(strings.TrimSpace(result.Stderr + "\n" + result.Stdout))
	return strings.Contains(detail, "no note found")
}
