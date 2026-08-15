package autosnap

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

type pendingExplainRow struct {
	checkpoint    checkpointInfo
	status        checkpointPendingStatus
	patchStatus   checkpointPatchStatus
	worktreeMatch checkpointWorktreeMatch
	mark          checkpointMark
	ready         bool
	patchReady    bool
}

type flushWriter interface {
	Flush()
}

func streamExplainPendingCheckpointRefs(ctx context.Context, repoRoot string, refs []checkpointRefInfo, branch string, allBranches bool, out io.Writer, useColor bool, debugLog pendingDebugLogger, includeNotes bool, noteRef string, includePatchStatus bool) error {
	if len(refs) == 0 {
		if allBranches {
			fmt.Fprintln(out, "no checkpoints")
		} else if strings.TrimSpace(branch) != "" {
			fmt.Fprintf(out, "no checkpoints for branch %s\n", strings.TrimSpace(branch))
		} else {
			fmt.Fprintln(out, "no checkpoints for current branch")
		}
		flushPendingOutput(out)
		return nil
	}

	debugLog.Printf("loading checkpoint metadata count=%d mode=explain", len(refs))
	checkpoints, err := listCheckpointsFromRefs(ctx, repoRoot, refs)
	if err != nil {
		return err
	}
	debugLog.Printf("loaded checkpoint metadata count=%d mode=explain", len(checkpoints))
	debugLog.Printf("loading worktree markers count=%d mode=explain", len(checkpoints))
	worktreeState, err := checkpointWorktreeMatchStateDebug(ctx, repoRoot, checkpoints, debugLog)
	if err != nil {
		return err
	}
	worktreeMatches := worktreeState.Matches
	debugLog.Printf("loaded worktree markers count=%d mode=explain", len(worktreeMatches))
	var patchClassifier *checkpointPatchStatusClassifier
	if includePatchStatus {
		debugLog.Printf("patch status classification started checkpoints=%d target_tree=%s mode=explain-stream", len(checkpoints), shortObjectID(worktreeState.WorktreeTree))
		patchClassifier, err = newCheckpointPatchStatusClassifier(ctx, repoRoot, branch, worktreeState.WorktreeTree, checkpoints)
		if err != nil {
			return err
		}
	}
	markStart := time.Now()
	debugLog.Printf("loading checkpoint marks count=%d mode=explain", len(checkpoints))
	marks, err := checkpointMarks(ctx, repoRoot, checkpoints)
	if err != nil {
		return err
	}
	debugLog.Printf("loaded checkpoint marks count=%d mode=explain elapsed=%s", len(marks), time.Since(markStart).Round(time.Millisecond))

	rowByRef := map[string]int{}
	rows := make([]pendingExplainRow, len(checkpoints))
	for i, cp := range checkpoints {
		rowByRef[cp.Ref] = i
		rows[i].checkpoint = cp
		rows[i].worktreeMatch = worktreeMatches[cp.Ref]
		rows[i].mark = marks[cp.Ref]
	}
	columns := checkpointTextColumns{
		BranchWidth:        checkpointTextBranchWidth(checkpoints, allBranches),
		CommitWidth:        checkpointTextCommitWidth(checkpoints),
		MarkWidth:          checkpointTextMarkWidth(checkpoints, marks),
		TimestampWidth:     checkpointTextTimestampWidth(checkpoints),
		IncludeStatus:      true,
		IncludePatchStatus: includePatchStatus,
	}

	classifiedCh := make(chan classifiedCheckpointRef)
	errCh := make(chan error, 1)
	go func() {
		defer close(classifiedCh)
		errCh <- classifyPendingCheckpointRefsStreamDebug(ctx, repoRoot, refs, branch, allBranches, debugLog, func(classified classifiedCheckpointRef) {
			classifiedCh <- classified
		})
	}()

	next := 0
	printed := 0
	classifyPatchStatus := func(index int) error {
		if !includePatchStatus || rows[index].patchReady {
			return nil
		}
		status, reason, elapsed, err := patchClassifier.Classify(ctx, index, rows[index].checkpoint)
		if err != nil {
			return err
		}
		rows[index].patchStatus = status
		rows[index].patchReady = true
		debugLog.Printf("patch status classified ref=%s commit=%s status=%s reason=%s elapsed=%s", rows[index].checkpoint.Ref, rows[index].checkpoint.Commit, status, reason, elapsed.Round(time.Millisecond))
		return nil
	}
	flushReadyRows := func() error {
		for next < len(rows) && rows[next].ready {
			if err := classifyPatchStatus(next); err != nil {
				return err
			}
			printPendingExplainRow(out, rows[next].checkpoint, rows[next].status, rows[next].patchStatus, rows[next].worktreeMatch, rows[next].mark, columns, useColor)
			if includeNotes {
				if err := writeCheckpointTextNote(ctx, repoRoot, out, noteRef, rows[next].checkpoint.Ref); err != nil {
					return err
				}
			}
			flushPendingOutput(out)
			next++
			printed++
		}
		return nil
	}

	for classified := range classifiedCh {
		index, ok := rowByRef[classified.Ref]
		if !ok {
			continue
		}
		rows[index].status = classified.Status
		if rows[index].status == "" {
			rows[index].status = checkpointStatusPending
		}
		rows[index].ready = true
		if err := flushReadyRows(); err != nil {
			return err
		}
	}

	if err := <-errCh; err != nil {
		return err
	}
	for next < len(rows) {
		if !rows[next].ready {
			next++
			continue
		}
		if err := classifyPatchStatus(next); err != nil {
			return err
		}
		printPendingExplainRow(out, rows[next].checkpoint, rows[next].status, rows[next].patchStatus, rows[next].worktreeMatch, rows[next].mark, columns, useColor)
		if includeNotes {
			if err := writeCheckpointTextNote(ctx, repoRoot, out, noteRef, rows[next].checkpoint.Ref); err != nil {
				return err
			}
		}
		flushPendingOutput(out)
		next++
		printed++
	}
	if printed == 0 {
		if allBranches {
			fmt.Fprintln(out, "no checkpoints")
		} else if strings.TrimSpace(branch) != "" {
			fmt.Fprintf(out, "no checkpoints for branch %s\n", strings.TrimSpace(branch))
		} else {
			fmt.Fprintln(out, "no checkpoints for current branch")
		}
		flushPendingOutput(out)
	}
	if includePatchStatus {
		patchClassifier.DebugSummary(debugLog, printed)
	}
	debugLog.Printf("streamed checkpoint metadata count=%d mode=explain", printed)
	return nil
}

func printPendingExplainRow(out io.Writer, cp checkpointInfo, status checkpointPendingStatus, patchStatus checkpointPatchStatus, match checkpointWorktreeMatch, mark checkpointMark, columns checkpointTextColumns, useColor bool) {
	fmt.Fprintln(out, formatCheckpointTextRow(checkpointTextRow{
		Branch:      cp.Branch,
		Timestamp:   formatCheckpointTimestampForList(cp.Timestamp),
		Status:      status,
		PatchStatus: patchStatus,
		Commit:      cp.Commit,
		Match:       match,
		Mark:        mark,
		Summary:     cp.Summary,
	}, columns, useColor))
}

func writePendingExplainJSON(ctx context.Context, repoRoot string, refs []checkpointRefInfo, branch string, allBranches bool, out io.Writer, debugLog pendingDebugLogger, opts checkpointJSONLOptions) error {
	if len(refs) == 0 {
		return writeCheckpointsJSON(ctx, repoRoot, out, nil, opts)
	}

	statusByRef, err := classifyPendingCheckpointRefsStreamMap(ctx, repoRoot, refs, branch, allBranches, debugLog)
	if err != nil {
		return err
	}
	opts.PendingStatus = statusByRef

	debugLog.Printf("loading checkpoint metadata count=%d mode=explain-json", len(refs))
	checkpoints, err := listCheckpointsFromRefs(ctx, repoRoot, refs)
	if err != nil {
		return err
	}
	debugLog.Printf("loaded checkpoint metadata count=%d mode=explain-json", len(checkpoints))
	debugLog.Printf("loading worktree markers count=%d mode=explain-json", len(checkpoints))
	worktreeState, err := checkpointWorktreeMatchStateDebug(ctx, repoRoot, checkpoints, debugLog)
	if err != nil {
		return err
	}
	opts.WorktreeMatches = worktreeState.Matches
	debugLog.Printf("loaded worktree markers count=%d mode=explain-json", len(opts.WorktreeMatches))
	if opts.IncludePatchStatus {
		patchStart := time.Now()
		debugLog.Printf("loading patch statuses count=%d mode=explain-json", len(checkpoints))
		opts.PatchStatus, err = checkpointPatchStatusesDebug(ctx, repoRoot, branch, worktreeState.WorktreeTree, checkpoints, debugLog)
		if err != nil {
			return err
		}
		debugLog.Printf("loaded patch statuses count=%d mode=explain-json elapsed=%s", len(opts.PatchStatus), time.Since(patchStart).Round(time.Millisecond))
	}
	markStart := time.Now()
	debugLog.Printf("loading checkpoint marks count=%d mode=explain-json", len(checkpoints))
	opts.Marks, err = checkpointMarks(ctx, repoRoot, checkpoints)
	if err != nil {
		return err
	}
	debugLog.Printf("loaded checkpoint marks count=%d mode=explain-json elapsed=%s", len(opts.Marks), time.Since(markStart).Round(time.Millisecond))
	return writeCheckpointsJSON(ctx, repoRoot, out, checkpoints, opts)
}

func writePendingExplainJSONL(ctx context.Context, repoRoot string, refs []checkpointRefInfo, branch string, allBranches bool, out io.Writer, debugLog pendingDebugLogger, opts checkpointJSONLOptions) error {
	if len(refs) == 0 {
		return nil
	}

	statusByRef, err := classifyPendingCheckpointRefsStreamMap(ctx, repoRoot, refs, branch, allBranches, debugLog)
	if err != nil {
		return err
	}
	opts.PendingStatus = statusByRef

	debugLog.Printf("loading checkpoint metadata count=%d mode=explain-jsonl", len(refs))
	checkpoints, err := listCheckpointsFromRefs(ctx, repoRoot, refs)
	if err != nil {
		return err
	}
	debugLog.Printf("loaded checkpoint metadata count=%d mode=explain-jsonl", len(checkpoints))
	debugLog.Printf("loading worktree markers count=%d mode=explain-jsonl", len(checkpoints))
	worktreeState, err := checkpointWorktreeMatchStateDebug(ctx, repoRoot, checkpoints, debugLog)
	if err != nil {
		return err
	}
	opts.WorktreeMatches = worktreeState.Matches
	debugLog.Printf("loaded worktree markers count=%d mode=explain-jsonl", len(opts.WorktreeMatches))
	if opts.IncludePatchStatus {
		patchStart := time.Now()
		debugLog.Printf("loading patch statuses count=%d mode=explain-jsonl", len(checkpoints))
		opts.PatchStatus, err = checkpointPatchStatusesDebug(ctx, repoRoot, branch, worktreeState.WorktreeTree, checkpoints, debugLog)
		if err != nil {
			return err
		}
		debugLog.Printf("loaded patch statuses count=%d mode=explain-jsonl elapsed=%s", len(opts.PatchStatus), time.Since(patchStart).Round(time.Millisecond))
	}
	markStart := time.Now()
	debugLog.Printf("loading checkpoint marks count=%d mode=explain-jsonl", len(checkpoints))
	opts.Marks, err = checkpointMarks(ctx, repoRoot, checkpoints)
	if err != nil {
		return err
	}
	debugLog.Printf("loaded checkpoint marks count=%d mode=explain-jsonl elapsed=%s", len(opts.Marks), time.Since(markStart).Round(time.Millisecond))
	return writeCheckpointsJSONL(ctx, repoRoot, out, checkpoints, opts)
}

func classifyPendingCheckpointRefsStreamMap(ctx context.Context, repoRoot string, refs []checkpointRefInfo, branch string, allBranches bool, debugLog pendingDebugLogger) (map[string]checkpointPendingStatus, error) {
	statusByRef := map[string]checkpointPendingStatus{}
	err := classifyPendingCheckpointRefsStreamDebug(ctx, repoRoot, refs, branch, allBranches, debugLog, func(classified classifiedCheckpointRef) {
		status := classified.Status
		if status == "" {
			status = checkpointStatusPending
		}
		statusByRef[classified.Ref] = status
	})
	if err != nil {
		return nil, err
	}
	return statusByRef, nil
}

func flushPendingOutput(out io.Writer) {
	if flusher, ok := out.(flushWriter); ok {
		flusher.Flush()
	}
}
