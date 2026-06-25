package autosnap

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newListCommand() *cobra.Command {
	var (
		allBranches bool
		branch      string
		format      string
		notes       bool
		notesJSON   bool
		noteRef     string
		since       string
	)

	cmd := &cobra.Command{
		Use:   "list [checkpoint-or-range]",
		Short: "List checkpoints",
		Long: strings.TrimSpace(`List checkpoints.

A range A..B lists checkpoints from A through B, inclusive. Ranges are
inclusive autosnap checkpoint intervals, not general Git revision ranges.

The checkpoint argument can be an explicit autosnap ref, a checkpoint commit hash,
or one of these current-branch history selectors:

  first
  first+N
  last
  last-N`),
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
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

			repoRoot, _, branchRef, err := detectRepository(ctx)
			if err != nil {
				return err
			}
			resolvedNoteRef := ""
			if includeNotes {
				resolvedNoteRef, err = resolveOutputNoteRef(repoRoot, noteRef)
				if err != nil {
					return err
				}
			}

			if strings.TrimSpace(branch) != "" && allBranches {
				return fmt.Errorf("list accepts at most one scope flag: --branch or --all")
			}
			if len(args) > 0 && allBranches {
				return fmt.Errorf("list accepts checkpoint ranges only for current branch or --branch")
			}

			var refs []checkpointRefInfo
			if len(args) > 0 {
				scopeBranch := branchRef
				if strings.TrimSpace(branch) != "" {
					scopeBranch = strings.TrimSpace(branch)
				}
				var refsErr error
				refs, refsErr = listCheckpointRefsForRange(ctx, repoRoot, scopeBranch, args[0])
				if refsErr != nil {
					return refsErr
				}
			} else {
				switch {
				case strings.TrimSpace(branch) != "":
					refs, err = listCheckpointRefsForBranch(ctx, repoRoot, strings.TrimSpace(branch))
				case allBranches:
					refs, err = listCheckpointRefsForAllBranches(ctx, repoRoot)
				default:
					refs, err = listCheckpointRefsForBranch(ctx, repoRoot, branchRef)
				}
			}
			if err != nil {
				return err
			}
			if strings.TrimSpace(since) != "" {
				refs, err = filterCheckpointRefsSince(ctx, repoRoot, refs, since, time.Now().UTC())
				if err != nil {
					return err
				}
			}

			checkpoints, err := listCheckpointsFromRefs(ctx, repoRoot, refs)
			if err != nil {
				return err
			}
			worktreeMatches, err := checkpointWorktreeMatches(ctx, repoRoot, checkpoints)
			if err != nil {
				return err
			}
			if normalizedFormat == outputFormatJSON {
				return writeCheckpointsJSON(ctx, repoRoot, out, checkpoints, checkpointJSONLOptions{
					IncludeNotes:    includeNotes,
					NotesJSON:       notesJSON,
					NoteRef:         resolvedNoteRef,
					WorktreeMatches: worktreeMatches,
				})
			}
			if normalizedFormat == outputFormatJSONL {
				if len(checkpoints) == 0 {
					return nil
				}
				return writeCheckpointsJSONL(ctx, repoRoot, out, checkpoints, checkpointJSONLOptions{
					IncludeNotes:    includeNotes,
					NotesJSON:       notesJSON,
					NoteRef:         resolvedNoteRef,
					WorktreeMatches: worktreeMatches,
				})
			}
			if len(checkpoints) == 0 {
				switch {
				case strings.TrimSpace(branch) != "":
					fmt.Fprintf(out, "no checkpoints for branch %s\n", strings.TrimSpace(branch))
				case allBranches:
					fmt.Fprintln(out, "no checkpoints")
				default:
					fmt.Fprintln(out, "no checkpoints for current branch")
				}
				return nil
			}

			for _, cp := range checkpoints {
				displayTimestamp := formatCheckpointTimestampForList(cp.Timestamp)
				marker := colorizeWorktreeMatchMarker(useColor, worktreeMatches[cp.Ref])
				ref := colorizeCheckpointID(useColor, cp.Commit)
				summary := colorizeCommitMessage(useColor, cp.Summary)
				if allBranches {
					fmt.Fprintf(out, "%s %s %s %s %s\n", cp.Branch, displayTimestamp, ref, marker, summary)
				} else {
					fmt.Fprintf(out, "%s %s %s %s\n", displayTimestamp, ref, marker, summary)
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

	cmd.Flags().StringVar(&branch, "branch", "", "List checkpoints for a specific branch")
	cmd.Flags().BoolVar(&allBranches, "all", false, "List checkpoints for all branches")
	cmd.Flags().StringVar(&format, "format", outputFormatText, "Output format: text, json, jsonl")
	cmd.Flags().BoolVar(&notes, "notes", false, "Include checkpoint git notes as text")
	cmd.Flags().BoolVar(&notesJSON, "notes-json", false, "Include checkpoint git notes decoded as JSON (requires --format json or jsonl)")
	cmd.Flags().StringVar(&noteRef, "note-ref", "", "Git notes ref for checkpoint notes")
	cmd.Flags().StringVar(&since, "since", "", "List checkpoints since a duration or commit ID")
	return cmd
}

func listCheckpointRefsForRange(ctx context.Context, repoRoot, branchRef, arg string) ([]checkpointRefInfo, error) {
	startArg, endArg, ranged, err := splitCheckpointRangeArg(arg)
	if err != nil {
		return nil, err
	}

	start, err := resolveShowCheckpointRefMetadata(ctx, repoRoot, branchRef, startArg)
	if err != nil {
		return nil, err
	}

	end := start
	if ranged {
		end, err = resolveShowCheckpointRefMetadata(ctx, repoRoot, branchRef, endArg)
		if err != nil {
			return nil, err
		}
	}

	startBranch := checkpointRefBranch(start, branchRef)
	endBranch := checkpointRefBranch(end, branchRef)
	if startBranch != endBranch {
		return nil, fmt.Errorf("checkpoint range endpoints must be on the same autosnap branch: %s and %s", startBranch, endBranch)
	}

	checkpoints, err := listCheckpointRefsForBranch(ctx, repoRoot, startBranch)
	if err != nil {
		return nil, err
	}

	startIndex := checkpointRefIndex(checkpoints, start.Ref)
	if startIndex < 0 {
		return nil, fmt.Errorf("checkpoint not found in branch history: %s", start.Ref)
	}
	endIndex := checkpointRefIndex(checkpoints, end.Ref)
	if endIndex < 0 {
		return nil, fmt.Errorf("checkpoint not found in branch history: %s", end.Ref)
	}
	if startIndex > endIndex {
		return nil, fmt.Errorf("checkpoint range start must not be after range end")
	}

	return append([]checkpointRefInfo(nil), checkpoints[startIndex:endIndex+1]...), nil
}

func formatCheckpointTimestampForList(timestamp string) string {
	return formatCheckpointTimestamp(timestamp)
}
