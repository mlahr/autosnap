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
	outputFormatJSON  = "json"
	outputFormatJSONL = "jsonl"
)

type checkpointJSONLOptions struct {
	IncludeNotes    bool
	NotesJSON       bool
	NoteRef         string
	PendingStatus   map[string]checkpointPendingStatus
	WorktreeMatches map[string]checkpointWorktreeMatch
}

func normalizeOutputFormat(format string) (string, error) {
	switch strings.TrimSpace(format) {
	case "", outputFormatText:
		return outputFormatText, nil
	case outputFormatJSON:
		return outputFormatJSON, nil
	case outputFormatJSONL:
		return outputFormatJSONL, nil
	default:
		return "", fmt.Errorf("invalid --format value %q (expected text, json, jsonl)", format)
	}
}

func validateCheckpointOutputOptions(format string, notes, notesJSON bool) error {
	if notes && notesJSON {
		return fmt.Errorf("--notes and --notes-json are mutually exclusive")
	}
	if notesJSON && format == outputFormatText {
		return fmt.Errorf("--notes-json requires --format json or jsonl")
	}
	return nil
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

	return "", fmt.Errorf("--notes requires --note-ref or configured note_ref")
}

func writeCheckpointsJSON(ctx context.Context, repoRoot string, out io.Writer, checkpoints []checkpointInfo, opts checkpointJSONLOptions) error {
	rows, err := checkpointJSONRows(ctx, repoRoot, checkpoints, opts)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	return encoder.Encode(rows)
}

func writeCheckpointsJSONL(ctx context.Context, repoRoot string, out io.Writer, checkpoints []checkpointInfo, opts checkpointJSONLOptions) error {
	encoder := json.NewEncoder(out)
	rows, err := checkpointJSONRows(ctx, repoRoot, checkpoints, opts)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return err
		}
	}
	return nil
}

func checkpointJSONRows(ctx context.Context, repoRoot string, checkpoints []checkpointInfo, opts checkpointJSONLOptions) ([]map[string]any, error) {
	rows := make([]map[string]any, 0, len(checkpoints))
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
		if match, ok := opts.WorktreeMatches[checkpoint.Ref]; ok {
			row["worktreeMatch"] = match
		}
		if opts.IncludeNotes {
			note, noteErr, err := readCheckpointNote(ctx, repoRoot, opts.NoteRef, checkpoint.Ref, opts.NotesJSON)
			if err != nil {
				return nil, err
			}
			row["noteRef"] = opts.NoteRef
			row["note"] = note
			if noteErr != "" {
				row["noteError"] = noteErr
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func readCheckpointNote(ctx context.Context, repoRoot, noteRef, checkpointRef string, parseJSON bool) (any, string, error) {
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
	if !parseJSON {
		return strings.TrimRight(result.Stdout, "\n"), "", nil
	}

	var payload any
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		return nil, fmt.Sprintf("invalid JSON note for %s in %s: %v", checkpointRef, noteRef, err), nil
	}
	return payload, "", nil
}

func readCheckpointNoteString(ctx context.Context, repoRoot, noteRef, checkpointRef string) (string, string, error) {
	note, noteErr, err := readCheckpointNote(ctx, repoRoot, noteRef, checkpointRef, false)
	if err != nil || noteErr != "" || note == nil {
		return "", noteErr, err
	}
	noteString, _ := note.(string)
	return noteString, "", nil
}

func writeCheckpointTextNote(ctx context.Context, repoRoot string, out io.Writer, noteRef, checkpointRef string) error {
	note, noteErr, err := readCheckpointNoteString(ctx, repoRoot, noteRef, checkpointRef)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "note_ref: %s\n", noteRef)
	if noteErr != "" {
		fmt.Fprintf(out, "note_error: %s\n", noteErr)
		return nil
	}
	if note == "" {
		fmt.Fprintln(out, "note: null")
		return nil
	}
	fmt.Fprintf(out, "note: %s\n", note)
	return nil
}

func isMissingGitNote(result commandResult) bool {
	if result.ExitCode == 0 {
		return false
	}
	detail := strings.ToLower(strings.TrimSpace(result.Stderr + "\n" + result.Stdout))
	return strings.Contains(detail, "no note found")
}
