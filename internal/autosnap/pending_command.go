package autosnap

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newPendingCommand() *cobra.Command {
	var (
		allBranches bool
		branch      string
		debug       bool
		explain     bool
		format      string
		limit       int
		notes       bool
		notesJSON   bool
		noteRef     string
		patchStatus bool
		since       string
	)

	cmd := &cobra.Command{
		Use:   "pending",
		Short: "List checkpoints after the latest checkpoint matching branch tip",
		Args:  cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			debugLog := newPendingDebugLogger(cmd.ErrOrStderr(), debug)
			ctx := context.Background()
			useColor := isTerminalWriter(out)
			includeNotes := notes || notesJSON
			normalizedFormat, err := normalizeOutputFormat(format)
			if err != nil {
				return err
			}
			if err := validateCheckpointOutputOptions(normalizedFormat, notes, notesJSON); err != nil {
				return err
			}

			scopeCount := 0
			if strings.TrimSpace(branch) != "" {
				scopeCount++
			}
			if allBranches {
				scopeCount++
			}
			if scopeCount > 1 {
				return fmt.Errorf("%s accepts at most one scope flag: --branch or --all", cmd.CalledAs())
			}
			if limit < 0 {
				return fmt.Errorf("--limit must be non-negative")
			}
			if patchStatus && !explain {
				return fmt.Errorf("--patch-status requires --explain")
			}

			debugLog.Printf("detecting repository")
			repoRoot, _, branchRef, err := detectRepository(ctx)
			if err != nil {
				return err
			}
			debugLog.Printf("repository detected root=%s current_branch=%s", repoRoot, branchRef)
			resolvedNoteRef := ""
			if includeNotes {
				resolvedNoteRef, err = resolveOutputNoteRef(repoRoot, noteRef)
				if err != nil {
					return err
				}
			}

			scope := "current branch " + branchRef
			if strings.TrimSpace(branch) != "" {
				scope = "branch " + strings.TrimSpace(branch)
			} else if allBranches {
				scope = "all branches"
			}
			debugLog.Printf("listing checkpoint refs scope=%s", scope)

			var refs []checkpointRefInfo
			switch {
			case strings.TrimSpace(branch) != "":
				refs, err = listCheckpointRefsForBranch(ctx, repoRoot, strings.TrimSpace(branch))
			case allBranches:
				refs, err = listCheckpointRefsForAllBranches(ctx, repoRoot)
			default:
				refs, err = listCheckpointRefsForBranch(ctx, repoRoot, branchRef)
			}
			if err != nil {
				return err
			}
			debugLog.Printf("listed checkpoint refs count=%d branches=%d", len(refs), countCheckpointBranches(refs))
			refs, err = filterCheckpointRefsForPending(ctx, repoRoot, refs, since, limit, time.Now().UTC())
			if err != nil {
				return err
			}
			debugLog.Printf("selected checkpoint refs count=%d branches=%d since=%q limit=%d", len(refs), countCheckpointBranches(refs), strings.TrimSpace(since), limit)

			if explain {
				if normalizedFormat == outputFormatJSON {
					return writePendingExplainJSON(ctx, repoRoot, refs, branch, allBranches, out, debugLog, checkpointJSONLOptions{
						IncludeNotes:       includeNotes,
						NotesJSON:          notesJSON,
						NoteRef:            resolvedNoteRef,
						IncludePatchStatus: patchStatus,
					})
				}
				if normalizedFormat == outputFormatJSONL {
					return writePendingExplainJSONL(ctx, repoRoot, refs, branch, allBranches, out, debugLog, checkpointJSONLOptions{
						IncludeNotes:       includeNotes,
						NotesJSON:          notesJSON,
						NoteRef:            resolvedNoteRef,
						IncludePatchStatus: patchStatus,
					})
				}
				debugLog.Printf("streaming explain checkpoints count=%d mode=explain", len(refs))
				err := streamExplainPendingCheckpointRefs(ctx, repoRoot, refs, branch, allBranches, out, useColor, debugLog, includeNotes, resolvedNoteRef, patchStatus)
				if err != nil {
					return err
				}
				return nil
			}

			debugLog.Printf("classifying actionable checkpoints count=%d mode=pending", len(refs))
			pendingRefs, err := actionablePendingCheckpointRefsDebug(ctx, repoRoot, refs, branch, allBranches, debugLog)
			if err != nil {
				return err
			}
			debugLog.Printf("classified actionable checkpoints count=%d mode=pending", len(pendingRefs))
			debugLog.Printf("loading checkpoint metadata count=%d mode=pending", len(pendingRefs))
			pending, err := listCheckpointsFromRefs(ctx, repoRoot, pendingRefs)
			if err != nil {
				return err
			}
			debugLog.Printf("loaded checkpoint metadata count=%d mode=pending", len(pending))
			debugLog.Printf("loading worktree markers count=%d mode=pending", len(pending))
			worktreeMatches, err := checkpointWorktreeMatchesDebug(ctx, repoRoot, pending, debugLog)
			if err != nil {
				return err
			}
			debugLog.Printf("loaded worktree markers count=%d mode=pending", len(worktreeMatches))
			markStart := time.Now()
			debugLog.Printf("loading checkpoint marks count=%d mode=pending", len(pending))
			marks, err := checkpointMarks(ctx, repoRoot, pending)
			if err != nil {
				return err
			}
			debugLog.Printf("loaded checkpoint marks count=%d mode=pending elapsed=%s", len(marks), time.Since(markStart).Round(time.Millisecond))

			if normalizedFormat == outputFormatJSON {
				return writeCheckpointsJSON(ctx, repoRoot, out, pending, checkpointJSONLOptions{
					IncludeNotes:    includeNotes,
					NotesJSON:       notesJSON,
					NoteRef:         resolvedNoteRef,
					WorktreeMatches: worktreeMatches,
					Marks:           marks,
				})
			}
			if normalizedFormat == outputFormatJSONL {
				return writeCheckpointsJSONL(ctx, repoRoot, out, pending, checkpointJSONLOptions{
					IncludeNotes:    includeNotes,
					NotesJSON:       notesJSON,
					NoteRef:         resolvedNoteRef,
					WorktreeMatches: worktreeMatches,
					Marks:           marks,
				})
			}
			if len(pending) == 0 {
				if allBranches {
					fmt.Fprintln(out, "no pending checkpoints")
				} else if strings.TrimSpace(branch) != "" {
					fmt.Fprintf(out, "no pending checkpoints for branch %s\n", strings.TrimSpace(branch))
				} else {
					fmt.Fprintln(out, "no pending checkpoints for current branch")
				}
				return nil
			}

			for _, cp := range pending {
				displayTimestamp := formatCheckpointTimestampForList(cp.Timestamp)
				marker := colorizeWorktreeMatchMarker(useColor, worktreeMatches[cp.Ref])
				commit := colorizeCheckpointID(useColor, cp.Commit)
				summary := checkpointMarkSummary(useColor, marks[cp.Ref], cp.Summary)
				if allBranches {
					fmt.Fprintf(out, "%s %s %s %s %s\n", cp.Branch, displayTimestamp, commit, marker, summary)
				} else {
					fmt.Fprintf(out, "%s %s %s %s\n", displayTimestamp, commit, marker, summary)
				}
				if includeNotes {
					if err := writeCheckpointTextNote(ctx, repoRoot, out, resolvedNoteRef, cp.Ref); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&branch, "branch", "", "List pending checkpoints for a specific branch")
	cmd.Flags().BoolVar(&allBranches, "all", false, "List pending checkpoints for all branches")
	cmd.Flags().BoolVar(&debug, "debug", false, "Show progress diagnostics on stderr")
	cmd.Flags().BoolVar(&explain, "explain", false, "Show integration status for all scanned checkpoints")
	cmd.Flags().StringVar(&format, "format", outputFormatText, "Output format: text, json, jsonl")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum number of newest checkpoints to scan (0 means unlimited)")
	cmd.Flags().BoolVar(&notes, "notes", false, "Include checkpoint git notes as text")
	cmd.Flags().BoolVar(&notesJSON, "notes-json", false, "Include checkpoint git notes decoded as JSON (requires --format json or jsonl)")
	cmd.Flags().StringVar(&noteRef, "note-ref", "", "Git notes ref for checkpoint notes")
	cmd.Flags().BoolVar(&patchStatus, "patch-status", false, "Include whether each checkpoint patch is included in the current worktree (requires --explain)")
	cmd.Flags().StringVar(&since, "since", "", "Scan checkpoints since a duration or commit ID")
	return cmd
}
