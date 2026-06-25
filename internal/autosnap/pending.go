package autosnap

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
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
			includeNotes := notes || notesJSON
			normalizedFormat, err := normalizeOutputFormat(format)
			if err != nil {
				return err
			}
			if err := validateCheckpointOutputOptions(normalizedFormat, notes, notesJSON); err != nil {
				return err
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
					return writePendingExplainJSON(ctx, repoRoot, refs, branch, allBranches, out, checkpointJSONLOptions{
						IncludeNotes: includeNotes,
						NotesJSON:    notesJSON,
						NoteRef:      resolvedNoteRef,
					})
				}
				if normalizedFormat == outputFormatJSONL {
					return writePendingExplainJSONL(ctx, repoRoot, refs, branch, allBranches, out, checkpointJSONLOptions{
						IncludeNotes: includeNotes,
						NotesJSON:    notesJSON,
						NoteRef:      resolvedNoteRef,
					})
				}
				debugLog.Printf("streaming explain checkpoints count=%d mode=explain", len(refs))
				err := streamExplainPendingCheckpointRefs(ctx, repoRoot, refs, branch, allBranches, out, debugLog, includeNotes, resolvedNoteRef)
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

			if normalizedFormat == outputFormatJSON {
				return writeCheckpointsJSON(ctx, repoRoot, out, pending, checkpointJSONLOptions{
					IncludeNotes: includeNotes,
					NotesJSON:    notesJSON,
					NoteRef:      resolvedNoteRef,
				})
			}
			if normalizedFormat == outputFormatJSONL {
				return writeCheckpointsJSONL(ctx, repoRoot, out, pending, checkpointJSONLOptions{
					IncludeNotes: includeNotes,
					NotesJSON:    notesJSON,
					NoteRef:      resolvedNoteRef,
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
				if allBranches {
					fmt.Fprintf(out, "%s %s %s %s %s\n", cp.Branch, displayTimestamp, cp.Ref, cp.Commit, cp.Summary)
				} else {
					fmt.Fprintf(out, "%s %s %s %s\n", displayTimestamp, cp.Ref, cp.Commit, cp.Summary)
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
	cmd.Flags().StringVar(&since, "since", "", "Scan checkpoints since a duration or commit ID")
	return cmd
}

func filterPendingCheckpointRefs(ctx context.Context, repoRoot string, checkpoints []checkpointRefInfo, branch string, allBranches bool) ([]checkpointRefInfo, error) {
	return actionablePendingCheckpointRefsDebug(ctx, repoRoot, checkpoints, branch, allBranches, pendingDebugLogger{})
}

type pendingDebugLogger struct {
	enabled bool
	out     io.Writer
	start   time.Time
	mu      *sync.Mutex
}

func newPendingDebugLogger(out io.Writer, enabled bool) pendingDebugLogger {
	return pendingDebugLogger{
		enabled: enabled,
		out:     out,
		start:   time.Now(),
		mu:      &sync.Mutex{},
	}
}

func (d pendingDebugLogger) Printf(format string, args ...any) {
	if !d.enabled || d.out == nil {
		return
	}
	elapsed := time.Since(d.start).Round(time.Millisecond)
	d.mu.Lock()
	defer d.mu.Unlock()
	fmt.Fprintf(d.out, "debug: pending: +%s "+format+"\n", append([]any{elapsed}, args...)...)
}

func countCheckpointBranches(refs []checkpointRefInfo) int {
	branches := map[string]struct{}{}
	for _, ref := range refs {
		branch := strings.TrimSpace(ref.Branch)
		if branch == "" {
			continue
		}
		branches[branch] = struct{}{}
	}
	return len(branches)
}

func filterCheckpointRefsForPending(ctx context.Context, repoRoot string, refs []checkpointRefInfo, since string, limit int, now time.Time) ([]checkpointRefInfo, error) {
	if limit < 0 {
		return nil, fmt.Errorf("--limit must be greater than or equal to 0")
	}

	selected := append([]checkpointRefInfo(nil), refs...)
	var err error
	if strings.TrimSpace(since) != "" {
		selected, err = filterCheckpointRefsSince(ctx, repoRoot, selected, since, now)
		if err != nil {
			return nil, err
		}
	}

	sortCheckpointRefsForFiltering(selected)
	if limit > 0 && len(selected) > limit {
		selected = selected[len(selected)-limit:]
	}
	return selected, nil
}

func filterCheckpointRefsSince(ctx context.Context, repoRoot string, refs []checkpointRefInfo, value string, now time.Time) ([]checkpointRefInfo, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return refs, nil
	}

	if duration, err := parsePendingSinceDuration(trimmed); err == nil {
		return checkpointRefsSinceTime(refs, now.UTC().Add(-duration))
	}

	commit, err := resolveCommitID(ctx, repoRoot, trimmed)
	if err != nil {
		return nil, fmt.Errorf("invalid --since value %q: not a duration or resolvable commit", value)
	}

	matchingCheckpointRefs := checkpointRefsMatchingCommit(refs, commit)
	if len(matchingCheckpointRefs) > 0 {
		sortCheckpointRefsForFiltering(matchingCheckpointRefs)
		cutoff, err := parseCheckpointTimestamp(matchingCheckpointRefs[0].Timestamp)
		if err != nil {
			return nil, err
		}
		return checkpointRefsSinceTime(refs, cutoff)
	}

	selected := make([]checkpointRefInfo, 0, len(refs))
	for _, ref := range refs {
		isAncestor, err := isAncestorOrSelf(ctx, repoRoot, commit, checkpointRefCommit(ref))
		if err != nil {
			return nil, err
		}
		if isAncestor {
			selected = append(selected, ref)
		}
	}
	return selected, nil
}

func parsePendingSinceDuration(value string) (time.Duration, error) {
	trimmed := strings.TrimSpace(value)
	if strings.HasSuffix(trimmed, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(trimmed, "d"))
		if err != nil || days < 0 {
			return 0, fmt.Errorf("invalid duration")
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}

	duration, err := time.ParseDuration(trimmed)
	if err != nil || duration < 0 {
		return 0, fmt.Errorf("invalid duration")
	}
	return duration, nil
}

func resolveCommitID(ctx context.Context, repoRoot, value string) (string, error) {
	result, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", "--verify", value+"^{commit}")
	if err != nil {
		return "", gitCommandError(err, result)
	}
	commit := strings.TrimSpace(result.Stdout)
	if commit == "" {
		return "", fmt.Errorf("empty commit")
	}
	return commit, nil
}

func checkpointRefsMatchingCommit(refs []checkpointRefInfo, commit string) []checkpointRefInfo {
	normalized := strings.ToLower(strings.TrimSpace(commit))
	matches := make([]checkpointRefInfo, 0)
	for _, ref := range refs {
		if strings.ToLower(checkpointRefCommit(ref)) == normalized {
			matches = append(matches, ref)
		}
	}
	return matches
}

func checkpointRefsSinceTime(refs []checkpointRefInfo, cutoff time.Time) ([]checkpointRefInfo, error) {
	selected := make([]checkpointRefInfo, 0, len(refs))
	for _, ref := range refs {
		timestamp, err := parseCheckpointTimestamp(ref.Timestamp)
		if err != nil {
			return nil, err
		}
		if !timestamp.Before(cutoff.UTC()) {
			selected = append(selected, ref)
		}
	}
	return selected, nil
}

func isAncestorOrSelf(ctx context.Context, repoRoot, ancestor, descendant string) (bool, error) {
	result, err := runGitCommand(ctx, repoRoot, nil, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	if result.ExitCode == 1 {
		return false, nil
	}
	return false, gitCommandError(err, result)
}

func sortCheckpointRefsForFiltering(refs []checkpointRefInfo) {
	sort.Slice(refs, func(i, j int) bool {
		leftTime, leftErr := parseCheckpointTimestamp(refs[i].Timestamp)
		rightTime, rightErr := parseCheckpointTimestamp(refs[j].Timestamp)
		if leftErr == nil && rightErr == nil && !leftTime.Equal(rightTime) {
			return leftTime.Before(rightTime)
		}
		if refs[i].Timestamp != refs[j].Timestamp {
			return refs[i].Timestamp < refs[j].Timestamp
		}
		if refs[i].Branch != refs[j].Branch {
			return refs[i].Branch < refs[j].Branch
		}
		return refs[i].Ref < refs[j].Ref
	})
}

type checkpointPendingStatus string

const (
	checkpointStatusExact      checkpointPendingStatus = "exact"
	checkpointStatusIntegrated checkpointPendingStatus = "integrated"
	checkpointStatusObsolete   checkpointPendingStatus = "obsolete"
	checkpointStatusPending    checkpointPendingStatus = "pending"
	checkpointStatusConflict   checkpointPendingStatus = "conflict"
	checkpointStatusStrict     checkpointPendingStatus = "strict"
)

type classifiedCheckpointRef struct {
	checkpointRefInfo
	Status checkpointPendingStatus
}

func actionablePendingCheckpointRefsDebug(ctx context.Context, repoRoot string, checkpoints []checkpointRefInfo, branch string, allBranches bool, debugLog pendingDebugLogger) ([]checkpointRefInfo, error) {
	grouped := map[string][]checkpointRefInfo{}
	branchOrder := []string{}
	for _, cp := range checkpoints {
		baseRef := strings.TrimSpace(branch)
		if allBranches || baseRef == "" {
			baseRef = cp.Branch
		}
		baseRef = strings.TrimSpace(baseRef)
		if baseRef == "" {
			continue
		}
		if _, ok := grouped[baseRef]; !ok {
			branchOrder = append(branchOrder, baseRef)
		}
		grouped[baseRef] = append(grouped[baseRef], cp)
	}

	if len(grouped) == 0 {
		debugLog.Printf("no checkpoint refs to classify after grouping")
		return nil, nil
	}
	debugLog.Printf("grouped checkpoint refs branches=%d mode=pending", len(branchOrder))

	type branchPending struct {
		index int
		refs  []checkpointRefInfo
		err   error
	}
	pendingByBranch := make([]branchPending, len(branchOrder))
	resultCh := make(chan branchPending, len(branchOrder))

	var wg sync.WaitGroup
	for i, baseRef := range branchOrder {
		cpList := grouped[baseRef]
		wg.Add(1)
		go func(index int, ref string, refs []checkpointRefInfo) {
			defer wg.Done()
			pending, err := actionablePendingRefsForBranchDebug(ctx, repoRoot, ref, refs, allBranches, debugLog)
			resultCh <- branchPending{index: index, refs: pending, err: err}
		}(i, baseRef, cpList)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	for result := range resultCh {
		if result.err != nil {
			return nil, result.err
		}
		pendingByBranch[result.index] = result
	}

	pending := make([]checkpointRefInfo, 0, len(checkpoints))
	for _, result := range pendingByBranch {
		pending = append(pending, result.refs...)
	}

	return pending, nil
}

func classifyPendingCheckpointRefs(ctx context.Context, repoRoot string, checkpoints []checkpointRefInfo, branch string, allBranches bool) ([]classifiedCheckpointRef, error) {
	return classifyPendingCheckpointRefsDebug(ctx, repoRoot, checkpoints, branch, allBranches, pendingDebugLogger{})
}

type pendingExplainRow struct {
	checkpoint checkpointInfo
	status     checkpointPendingStatus
	ready      bool
}

type flushWriter interface {
	Flush()
}

func streamExplainPendingCheckpointRefs(ctx context.Context, repoRoot string, refs []checkpointRefInfo, branch string, allBranches bool, out io.Writer, debugLog pendingDebugLogger, includeNotes bool, noteRef string) error {
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

	rowByRef := map[string]int{}
	rows := make([]pendingExplainRow, len(checkpoints))
	for i, cp := range checkpoints {
		rowByRef[cp.Ref] = i
		rows[i].checkpoint = cp
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
	flushReadyRows := func() error {
		for next < len(rows) && rows[next].ready {
			printPendingExplainRow(out, rows[next].checkpoint, rows[next].status, allBranches)
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
		printPendingExplainRow(out, rows[next].checkpoint, rows[next].status, allBranches)
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
	debugLog.Printf("streamed checkpoint metadata count=%d mode=explain", printed)
	return nil
}

func printPendingExplainRow(out io.Writer, cp checkpointInfo, status checkpointPendingStatus, allBranches bool) {
	displayTimestamp := formatCheckpointTimestampForList(cp.Timestamp)
	if allBranches {
		fmt.Fprintf(out, "%s %s %s %s %s %s\n", cp.Branch, displayTimestamp, status, cp.Ref, cp.Commit, cp.Summary)
		return
	}
	fmt.Fprintf(out, "%s %s %s %s %s\n", displayTimestamp, status, cp.Ref, cp.Commit, cp.Summary)
}

func writePendingExplainJSON(ctx context.Context, repoRoot string, refs []checkpointRefInfo, branch string, allBranches bool, out io.Writer, opts checkpointJSONLOptions) error {
	if len(refs) == 0 {
		return writeCheckpointsJSON(ctx, repoRoot, out, nil, opts)
	}

	statusByRef, err := classifyPendingCheckpointRefsStreamMap(ctx, repoRoot, refs, branch, allBranches, pendingDebugLogger{})
	if err != nil {
		return err
	}
	opts.PendingStatus = statusByRef

	checkpoints, err := listCheckpointsFromRefs(ctx, repoRoot, refs)
	if err != nil {
		return err
	}
	return writeCheckpointsJSON(ctx, repoRoot, out, checkpoints, opts)
}

func writePendingExplainJSONL(ctx context.Context, repoRoot string, refs []checkpointRefInfo, branch string, allBranches bool, out io.Writer, opts checkpointJSONLOptions) error {
	if len(refs) == 0 {
		return nil
	}

	statusByRef, err := classifyPendingCheckpointRefsStreamMap(ctx, repoRoot, refs, branch, allBranches, pendingDebugLogger{})
	if err != nil {
		return err
	}
	opts.PendingStatus = statusByRef

	checkpoints, err := listCheckpointsFromRefs(ctx, repoRoot, refs)
	if err != nil {
		return err
	}
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

func classifyPendingCheckpointRefsStreamDebug(ctx context.Context, repoRoot string, checkpoints []checkpointRefInfo, branch string, allBranches bool, debugLog pendingDebugLogger, emit func(classifiedCheckpointRef)) error {
	grouped := map[string][]checkpointRefInfo{}
	branchOrder := []string{}
	for _, cp := range checkpoints {
		baseRef := strings.TrimSpace(branch)
		if allBranches || baseRef == "" {
			baseRef = cp.Branch
		}
		baseRef = strings.TrimSpace(baseRef)
		if baseRef == "" {
			continue
		}
		if _, ok := grouped[baseRef]; !ok {
			branchOrder = append(branchOrder, baseRef)
		}
		grouped[baseRef] = append(grouped[baseRef], cp)
	}

	if len(grouped) == 0 {
		debugLog.Printf("no checkpoint refs to classify after grouping")
		return nil
	}
	debugLog.Printf("grouped checkpoint refs branches=%d", len(branchOrder))

	var wg sync.WaitGroup
	errCh := make(chan error, len(branchOrder))
	for _, baseRef := range branchOrder {
		cpList := grouped[baseRef]
		wg.Add(1)
		go func(ref string, refs []checkpointRefInfo) {
			defer wg.Done()
			if err := classifyPendingRefsForBranchStreamDebug(ctx, repoRoot, ref, refs, allBranches, debugLog, emit); err != nil {
				errCh <- err
			}
		}(baseRef, cpList)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

func classifyPendingCheckpointRefsDebug(ctx context.Context, repoRoot string, checkpoints []checkpointRefInfo, branch string, allBranches bool, debugLog pendingDebugLogger) ([]classifiedCheckpointRef, error) {
	grouped := map[string][]checkpointRefInfo{}
	branchOrder := []string{}
	for _, cp := range checkpoints {
		baseRef := strings.TrimSpace(branch)
		if allBranches || baseRef == "" {
			baseRef = cp.Branch
		}
		baseRef = strings.TrimSpace(baseRef)
		if baseRef == "" {
			continue
		}
		if _, ok := grouped[baseRef]; !ok {
			branchOrder = append(branchOrder, baseRef)
		}
		grouped[baseRef] = append(grouped[baseRef], cp)
	}

	if len(grouped) == 0 {
		debugLog.Printf("no checkpoint refs to classify after grouping")
		return nil, nil
	}
	debugLog.Printf("grouped checkpoint refs branches=%d", len(branchOrder))

	type branchPending struct {
		index     int
		checkrefs []classifiedCheckpointRef
		err       error
	}
	pendingByBranch := make([]branchPending, len(branchOrder))
	resultCh := make(chan branchPending, len(branchOrder))

	var wg sync.WaitGroup
	for i, baseRef := range branchOrder {
		cpList := grouped[baseRef]
		wg.Add(1)
		go func(index int, ref string, refs []checkpointRefInfo) {
			defer wg.Done()
			pending, err := classifyPendingRefsForBranchDebug(ctx, repoRoot, ref, refs, allBranches, debugLog)
			resultCh <- branchPending{index: index, checkrefs: pending, err: err}
		}(i, baseRef, cpList)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	for result := range resultCh {
		if result.err != nil {
			return nil, result.err
		}
		pendingByBranch[result.index] = result
	}

	pending := make([]classifiedCheckpointRef, 0, len(checkpoints))
	for _, result := range pendingByBranch {
		pending = append(pending, result.checkrefs...)
	}

	return pending, nil
}

func filterPendingRefsForBranch(ctx context.Context, repoRoot, baseRef string, checkpoints []checkpointRefInfo, allBranches bool) ([]checkpointRefInfo, error) {
	return actionablePendingRefsForBranchDebug(ctx, repoRoot, baseRef, checkpoints, allBranches, pendingDebugLogger{})
}

func classifyPendingRefsForBranch(ctx context.Context, repoRoot, baseRef string, checkpoints []checkpointRefInfo, allBranches bool) ([]classifiedCheckpointRef, error) {
	return classifyPendingRefsForBranchDebug(ctx, repoRoot, baseRef, checkpoints, allBranches, pendingDebugLogger{})
}

func classifyPendingRefsForBranchStreamDebug(ctx context.Context, repoRoot, baseRef string, checkpoints []checkpointRefInfo, allBranches bool, debugLog pendingDebugLogger, emit func(classifiedCheckpointRef)) error {
	if len(checkpoints) == 0 {
		return nil
	}
	debugLog.Printf("branch classification started branch=%s checkpoints=%d order=newest-first mode=explain", baseRef, len(checkpoints))
	branchStart := time.Now()

	resolvedRef := baseRef
	if strings.HasPrefix(resolvedRef, "detached-") {
		resolvedRef = "HEAD"
	}

	result, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", resolvedRef)
	if err != nil {
		if allBranches {
			debugLog.Printf("branch classification skipped branch=%s reason=unresolved", baseRef)
			return nil
		}
		return fmt.Errorf("failed to resolve branch tip %q: %w", resolvedRef, gitCommandError(err, result))
	}
	branchTip := strings.TrimSpace(result.Stdout)
	if branchTip == "" {
		if allBranches {
			debugLog.Printf("branch classification skipped branch=%s reason=empty_tip", baseRef)
			return nil
		}
		return fmt.Errorf("failed to resolve branch tip %q", resolvedRef)
	}
	debugLog.Printf("resolved branch tip branch=%s ref=%s commit=%s", baseRef, resolvedRef, shortObjectID(branchTip))

	branchTipTreeResult, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", branchTip+"^{tree}")
	if err != nil {
		return fmt.Errorf("failed to resolve branch tip tree %q: %w", resolvedRef, gitCommandError(err, branchTipTreeResult))
	}
	branchTipTree := strings.TrimSpace(branchTipTreeResult.Stdout)
	if branchTipTree == "" {
		return fmt.Errorf("failed to resolve branch tip tree %q", resolvedRef)
	}
	debugLog.Printf("resolved branch tip tree branch=%s tree=%s", baseRef, shortObjectID(branchTipTree))

	debugLog.Printf("resolving checkpoint trees branch=%s count=%d", baseRef, len(checkpoints))
	revParseArgs := make([]string, 0, len(checkpoints)+1)
	revParseArgs = append(revParseArgs, "rev-parse")
	for _, cp := range checkpoints {
		revParseArgs = append(revParseArgs, checkpointRefCommit(cp)+"^{tree}")
	}
	treesResult, err := runGitCommand(ctx, repoRoot, nil, revParseArgs...)
	if err != nil {
		return gitCommandError(err, treesResult)
	}

	treeLines := strings.Split(strings.TrimSpace(treesResult.Stdout), "\n")
	if len(treeLines) != len(checkpoints) {
		debugLog.Printf("checkpoint tree batch count mismatch branch=%s expected=%d got=%d; falling back to per-checkpoint resolution", baseRef, len(checkpoints), len(treeLines))
		treeLines = make([]string, len(checkpoints))
		for i, cp := range checkpoints {
			tree, treeErr := getCheckpointTree(ctx, repoRoot, checkpointRefCommit(cp))
			if treeErr != nil {
				return treeErr
			}
			treeLines[i] = strings.TrimSpace(tree)
		}
	}
	debugLog.Printf("resolved checkpoint trees branch=%s count=%d", baseRef, len(treeLines))

	emitted := make([]bool, len(checkpoints))
	emitAt := func(i int, status checkpointPendingStatus) {
		if i < 0 || i >= len(checkpoints) || emitted[i] {
			return
		}
		emitted[i] = true
		emit(classifiedCheckpointRef{
			checkpointRefInfo: checkpoints[i],
			Status:            status,
		})
	}

	for i := len(checkpoints) - 1; i >= 0; i-- {
		cp := checkpoints[i]
		if i < len(treeLines) && strings.TrimSpace(treeLines[i]) == branchTipTree {
			debugLog.Printf("checkpoint classified branch=%s index=%d/%d ref=%s commit=%s status=%s reason=tree_exact", baseRef, i+1, len(checkpoints), cp.Ref, cp.Commit, checkpointStatusExact)
			emitAt(i, checkpointStatusExact)
			for older := 0; older < i; older++ {
				status := checkpointStatusObsolete
				if older < len(treeLines) && strings.TrimSpace(treeLines[older]) == branchTipTree {
					status = checkpointStatusExact
				}
				debugLog.Printf("checkpoint classified branch=%s index=%d/%d ref=%s commit=%s status=%s", baseRef, older+1, len(checkpoints), checkpoints[older].Ref, checkpoints[older].Commit, status)
				emitAt(older, status)
			}
			debugLog.Printf("branch classification finished branch=%s classified=%d elapsed=%s", baseRef, len(checkpoints), time.Since(branchStart).Round(time.Millisecond))
			return nil
		}

		debugLog.Printf("merge classification started branch=%s index=%d/%d ref=%s commit=%s", baseRef, i+1, len(checkpoints), cp.Ref, cp.Commit)
		mergeStatus, unsupported, err := classifyCheckpointMergeStatusDebug(ctx, repoRoot, branchTip, branchTipTree, cp, debugLog)
		if err != nil {
			return err
		}
		if unsupported {
			debugLog.Printf("merge-tree --write-tree unsupported; using strict classification branch=%s", baseRef)
			for _, classified := range classifyPendingRefsStrict(checkpoints, treeLines, branchTipTree) {
				emit(classified)
			}
			debugLog.Printf("branch classification finished branch=%s classified=%d elapsed=%s", baseRef, len(checkpoints), time.Since(branchStart).Round(time.Millisecond))
			return nil
		}
		debugLog.Printf("checkpoint classified branch=%s index=%d/%d ref=%s commit=%s status=%s", baseRef, i+1, len(checkpoints), cp.Ref, cp.Commit, mergeStatus)
		emitAt(i, mergeStatus)
		if mergeStatus == checkpointStatusIntegrated {
			for older := 0; older < i; older++ {
				status := checkpointStatusObsolete
				if older < len(treeLines) && strings.TrimSpace(treeLines[older]) == branchTipTree {
					status = checkpointStatusExact
				}
				debugLog.Printf("checkpoint classified branch=%s index=%d/%d ref=%s commit=%s status=%s", baseRef, older+1, len(checkpoints), checkpoints[older].Ref, checkpoints[older].Commit, status)
				emitAt(older, status)
			}
			debugLog.Printf("branch classification finished branch=%s classified=%d elapsed=%s", baseRef, len(checkpoints), time.Since(branchStart).Round(time.Millisecond))
			return nil
		}
	}

	debugLog.Printf("branch classification finished branch=%s classified=%d elapsed=%s", baseRef, len(checkpoints), time.Since(branchStart).Round(time.Millisecond))
	return nil
}

func classifyPendingRefsForBranchDebug(ctx context.Context, repoRoot, baseRef string, checkpoints []checkpointRefInfo, allBranches bool, debugLog pendingDebugLogger) ([]classifiedCheckpointRef, error) {
	if len(checkpoints) == 0 {
		return nil, nil
	}
	debugLog.Printf("branch classification started branch=%s checkpoints=%d", baseRef, len(checkpoints))
	branchStart := time.Now()

	resolvedRef := baseRef
	if strings.HasPrefix(resolvedRef, "detached-") {
		resolvedRef = "HEAD"
	}

	result, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", resolvedRef)
	if err != nil {
		if allBranches {
			debugLog.Printf("branch classification skipped branch=%s reason=unresolved", baseRef)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to resolve branch tip %q: %w", resolvedRef, gitCommandError(err, result))
	}
	branchTip := strings.TrimSpace(result.Stdout)
	if branchTip == "" {
		if allBranches {
			debugLog.Printf("branch classification skipped branch=%s reason=empty_tip", baseRef)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to resolve branch tip %q", resolvedRef)
	}
	debugLog.Printf("resolved branch tip branch=%s ref=%s commit=%s", baseRef, resolvedRef, shortObjectID(branchTip))

	branchTipTreeResult, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", branchTip+"^{tree}")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve branch tip tree %q: %w", resolvedRef, gitCommandError(err, branchTipTreeResult))
	}
	branchTipTree := strings.TrimSpace(branchTipTreeResult.Stdout)
	if branchTipTree == "" {
		return nil, fmt.Errorf("failed to resolve branch tip tree %q", resolvedRef)
	}
	debugLog.Printf("resolved branch tip tree branch=%s tree=%s", baseRef, shortObjectID(branchTipTree))

	debugLog.Printf("resolving checkpoint trees branch=%s count=%d", baseRef, len(checkpoints))
	revParseArgs := make([]string, 0, len(checkpoints)+1)
	revParseArgs = append(revParseArgs, "rev-parse")
	for _, cp := range checkpoints {
		revParseArgs = append(revParseArgs, checkpointRefCommit(cp)+"^{tree}")
	}
	treesResult, err := runGitCommand(ctx, repoRoot, nil, revParseArgs...)
	if err != nil {
		return nil, gitCommandError(err, treesResult)
	}

	treeLines := strings.Split(strings.TrimSpace(treesResult.Stdout), "\n")
	if len(treeLines) != len(checkpoints) {
		debugLog.Printf("checkpoint tree batch count mismatch branch=%s expected=%d got=%d; falling back to per-checkpoint resolution", baseRef, len(checkpoints), len(treeLines))
		treeLines = make([]string, len(checkpoints))
		for i, cp := range checkpoints {
			tree, treeErr := getCheckpointTree(ctx, repoRoot, checkpointRefCommit(cp))
			if treeErr != nil {
				return nil, treeErr
			}
			treeLines[i] = strings.TrimSpace(tree)
		}
	}
	debugLog.Printf("resolved checkpoint trees branch=%s count=%d", baseRef, len(treeLines))

	classified := make([]classifiedCheckpointRef, 0, len(checkpoints))
	newestIntegratedIndex := -1
	for i, cp := range checkpoints {
		status := checkpointStatusPending
		if i < len(treeLines) && strings.TrimSpace(treeLines[i]) == branchTipTree {
			status = checkpointStatusExact
			newestIntegratedIndex = i
			debugLog.Printf("checkpoint classified branch=%s index=%d/%d ref=%s commit=%s status=%s reason=tree_exact", baseRef, i+1, len(checkpoints), cp.Ref, cp.Commit, status)
		} else {
			debugLog.Printf("merge classification started branch=%s index=%d/%d ref=%s commit=%s", baseRef, i+1, len(checkpoints), cp.Ref, cp.Commit)
			mergeStatus, unsupported, err := classifyCheckpointMergeStatusDebug(ctx, repoRoot, branchTip, branchTipTree, cp, debugLog)
			if err != nil {
				return nil, err
			}
			if unsupported {
				debugLog.Printf("merge-tree --write-tree unsupported; using strict classification branch=%s", baseRef)
				return classifyPendingRefsStrict(checkpoints, treeLines, branchTipTree), nil
			}
			status = mergeStatus
			debugLog.Printf("checkpoint classified branch=%s index=%d/%d ref=%s commit=%s status=%s", baseRef, i+1, len(checkpoints), cp.Ref, cp.Commit, status)
			if status == checkpointStatusIntegrated {
				newestIntegratedIndex = i
			}
		}
		classified = append(classified, classifiedCheckpointRef{
			checkpointRefInfo: cp,
			Status:            status,
		})
	}

	if newestIntegratedIndex >= 0 {
		for i := 0; i < newestIntegratedIndex; i++ {
			if classified[i].Status != checkpointStatusExact && classified[i].Status != checkpointStatusIntegrated {
				classified[i].Status = checkpointStatusObsolete
			}
		}
	}
	debugLog.Printf("branch classification finished branch=%s classified=%d elapsed=%s", baseRef, len(classified), time.Since(branchStart).Round(time.Millisecond))

	return classified, nil
}

func actionablePendingRefsForBranchDebug(ctx context.Context, repoRoot, baseRef string, checkpoints []checkpointRefInfo, allBranches bool, debugLog pendingDebugLogger) ([]checkpointRefInfo, error) {
	if len(checkpoints) == 0 {
		return nil, nil
	}
	debugLog.Printf("branch actionable classification started branch=%s checkpoints=%d order=newest-first", baseRef, len(checkpoints))
	branchStart := time.Now()

	resolvedRef := baseRef
	if strings.HasPrefix(resolvedRef, "detached-") {
		resolvedRef = "HEAD"
	}

	result, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", resolvedRef)
	if err != nil {
		if allBranches {
			debugLog.Printf("branch actionable classification skipped branch=%s reason=unresolved", baseRef)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to resolve branch tip %q: %w", resolvedRef, gitCommandError(err, result))
	}
	branchTip := strings.TrimSpace(result.Stdout)
	if branchTip == "" {
		if allBranches {
			debugLog.Printf("branch actionable classification skipped branch=%s reason=empty_tip", baseRef)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to resolve branch tip %q", resolvedRef)
	}
	debugLog.Printf("resolved branch tip branch=%s ref=%s commit=%s", baseRef, resolvedRef, shortObjectID(branchTip))

	branchTipTreeResult, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", branchTip+"^{tree}")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve branch tip tree %q: %w", resolvedRef, gitCommandError(err, branchTipTreeResult))
	}
	branchTipTree := strings.TrimSpace(branchTipTreeResult.Stdout)
	if branchTipTree == "" {
		return nil, fmt.Errorf("failed to resolve branch tip tree %q", resolvedRef)
	}
	debugLog.Printf("resolved branch tip tree branch=%s tree=%s", baseRef, shortObjectID(branchTipTree))

	debugLog.Printf("resolving checkpoint trees branch=%s count=%d", baseRef, len(checkpoints))
	revParseArgs := make([]string, 0, len(checkpoints)+1)
	revParseArgs = append(revParseArgs, "rev-parse")
	for _, cp := range checkpoints {
		revParseArgs = append(revParseArgs, checkpointRefCommit(cp)+"^{tree}")
	}
	treesResult, err := runGitCommand(ctx, repoRoot, nil, revParseArgs...)
	if err != nil {
		return nil, gitCommandError(err, treesResult)
	}

	treeLines := strings.Split(strings.TrimSpace(treesResult.Stdout), "\n")
	if len(treeLines) != len(checkpoints) {
		debugLog.Printf("checkpoint tree batch count mismatch branch=%s expected=%d got=%d; falling back to per-checkpoint resolution", baseRef, len(checkpoints), len(treeLines))
		treeLines = make([]string, len(checkpoints))
		for i, cp := range checkpoints {
			tree, treeErr := getCheckpointTree(ctx, repoRoot, checkpointRefCommit(cp))
			if treeErr != nil {
				return nil, treeErr
			}
			treeLines[i] = strings.TrimSpace(tree)
		}
	}
	debugLog.Printf("resolved checkpoint trees branch=%s count=%d", baseRef, len(treeLines))

	actionableByIndex := make([]bool, len(checkpoints))
	boundaryIndex := -1
	for i := len(checkpoints) - 1; i >= 0; i-- {
		cp := checkpoints[i]
		if i < len(treeLines) && strings.TrimSpace(treeLines[i]) == branchTipTree {
			boundaryIndex = i
			debugLog.Printf("checkpoint classified branch=%s index=%d/%d ref=%s commit=%s status=%s reason=tree_exact", baseRef, i+1, len(checkpoints), cp.Ref, cp.Commit, checkpointStatusExact)
			debugLog.Printf("early stop branch=%s index=%d/%d status=%s older_checkpoints=%d", baseRef, i+1, len(checkpoints), checkpointStatusExact, i)
			break
		}

		debugLog.Printf("merge classification started branch=%s index=%d/%d ref=%s commit=%s", baseRef, i+1, len(checkpoints), cp.Ref, cp.Commit)
		mergeStatus, unsupported, err := classifyCheckpointMergeStatusDebug(ctx, repoRoot, branchTip, branchTipTree, cp, debugLog)
		if err != nil {
			return nil, err
		}
		if unsupported {
			debugLog.Printf("merge-tree --write-tree unsupported; using strict actionable classification branch=%s", baseRef)
			pending := actionablePendingRefsStrict(checkpoints, treeLines, branchTipTree)
			debugLog.Printf("branch actionable classification finished branch=%s actionable=%d elapsed=%s", baseRef, len(pending), time.Since(branchStart).Round(time.Millisecond))
			return pending, nil
		}
		debugLog.Printf("checkpoint classified branch=%s index=%d/%d ref=%s commit=%s status=%s", baseRef, i+1, len(checkpoints), cp.Ref, cp.Commit, mergeStatus)
		if mergeStatus == checkpointStatusIntegrated {
			boundaryIndex = i
			debugLog.Printf("early stop branch=%s index=%d/%d status=%s older_checkpoints=%d", baseRef, i+1, len(checkpoints), mergeStatus, i)
			break
		}
		switch mergeStatus {
		case checkpointStatusPending, checkpointStatusConflict, checkpointStatusStrict:
			actionableByIndex[i] = true
		}
	}

	pending := make([]checkpointRefInfo, 0, len(checkpoints))
	for i, cp := range checkpoints {
		if boundaryIndex >= 0 && i <= boundaryIndex {
			continue
		}
		if actionableByIndex[i] {
			pending = append(pending, cp)
		}
	}
	debugLog.Printf("branch actionable classification finished branch=%s actionable=%d elapsed=%s", baseRef, len(pending), time.Since(branchStart).Round(time.Millisecond))

	return pending, nil
}

func classifyCheckpointMergeStatus(ctx context.Context, repoRoot, branchTip, branchTipTree string, cp checkpointRefInfo) (checkpointPendingStatus, bool, error) {
	return classifyCheckpointMergeStatusDebug(ctx, repoRoot, branchTip, branchTipTree, cp, pendingDebugLogger{})
}

func classifyCheckpointMergeStatusDebug(ctx context.Context, repoRoot, branchTip, branchTipTree string, cp checkpointRefInfo, debugLog pendingDebugLogger) (checkpointPendingStatus, bool, error) {
	start := time.Now()
	result, err := runGitCommand(ctx, repoRoot, nil, "merge-tree", "--write-tree", branchTip, checkpointRefCommit(cp))
	if err != nil && isMergeTreeWriteTreeUnsupported(result) {
		debugLog.Printf("merge classification finished ref=%s commit=%s status=%s unsupported=true elapsed=%s", cp.Ref, cp.Commit, checkpointStatusStrict, time.Since(start).Round(time.Millisecond))
		return checkpointStatusStrict, true, nil
	}

	firstLine := firstNonEmptyLine(result.Stdout)
	if firstLine == "" {
		firstLine = firstNonEmptyLine(result.Stderr)
	}
	if err == nil {
		if firstLine == branchTipTree {
			debugLog.Printf("merge classification finished ref=%s commit=%s status=%s elapsed=%s", cp.Ref, cp.Commit, checkpointStatusIntegrated, time.Since(start).Round(time.Millisecond))
			return checkpointStatusIntegrated, false, nil
		}
		debugLog.Printf("merge classification finished ref=%s commit=%s status=%s elapsed=%s", cp.Ref, cp.Commit, checkpointStatusPending, time.Since(start).Round(time.Millisecond))
		return checkpointStatusPending, false, nil
	}
	if firstLine != "" && looksLikeObjectID(firstLine) {
		debugLog.Printf("merge classification finished ref=%s commit=%s status=%s elapsed=%s", cp.Ref, cp.Commit, checkpointStatusConflict, time.Since(start).Round(time.Millisecond))
		return checkpointStatusConflict, false, nil
	}
	debugLog.Printf("merge classification finished ref=%s commit=%s status=%s elapsed=%s", cp.Ref, cp.Commit, checkpointStatusConflict, time.Since(start).Round(time.Millisecond))
	return checkpointStatusConflict, false, nil
}

func shortObjectID(objectID string) string {
	if len(objectID) <= 12 {
		return objectID
	}
	return objectID[:12]
}

func classifyPendingRefsStrict(checkpoints []checkpointRefInfo, treeLines []string, branchTipTree string) []classifiedCheckpointRef {
	syncedIndex := -1
	for i := len(checkpoints) - 1; i >= 0; i-- {
		if i >= len(treeLines) {
			continue
		}
		if strings.TrimSpace(treeLines[i]) == "" {
			continue
		}
		if strings.TrimSpace(treeLines[i]) == branchTipTree {
			syncedIndex = i
			break
		}
	}

	classified := make([]classifiedCheckpointRef, 0, len(checkpoints))
	for i, cp := range checkpoints {
		status := checkpointStatusStrict
		if i < len(treeLines) && strings.TrimSpace(treeLines[i]) == branchTipTree {
			status = checkpointStatusExact
		} else if i <= syncedIndex {
			status = checkpointStatusObsolete
		}
		classified = append(classified, classifiedCheckpointRef{
			checkpointRefInfo: cp,
			Status:            status,
		})
	}
	return classified
}

func actionablePendingRefsStrict(checkpoints []checkpointRefInfo, treeLines []string, branchTipTree string) []checkpointRefInfo {
	syncedIndex := -1
	for i := len(checkpoints) - 1; i >= 0; i-- {
		if i >= len(treeLines) {
			continue
		}
		if strings.TrimSpace(treeLines[i]) == "" {
			continue
		}
		if strings.TrimSpace(treeLines[i]) == branchTipTree {
			syncedIndex = i
			break
		}
	}

	pending := make([]checkpointRefInfo, 0, len(checkpoints))
	for i, cp := range checkpoints {
		if i <= syncedIndex {
			continue
		}
		if i < len(treeLines) && strings.TrimSpace(treeLines[i]) == branchTipTree {
			continue
		}
		pending = append(pending, cp)
	}
	return pending
}

func actionableCheckpointRefs(classified []classifiedCheckpointRef) []checkpointRefInfo {
	pending := make([]checkpointRefInfo, 0, len(classified))
	for _, cp := range classified {
		switch cp.Status {
		case checkpointStatusPending, checkpointStatusConflict, checkpointStatusStrict:
			pending = append(pending, cp.checkpointRefInfo)
		}
	}
	return pending
}

func classifiedCheckpointRefs(classified []classifiedCheckpointRef) []checkpointRefInfo {
	refs := make([]checkpointRefInfo, 0, len(classified))
	for _, cp := range classified {
		refs = append(refs, cp.checkpointRefInfo)
	}
	return refs
}

func checkpointStatusByRef(classified []classifiedCheckpointRef) map[string]checkpointPendingStatus {
	statusByRef := map[string]checkpointPendingStatus{}
	for _, cp := range classified {
		statusByRef[cp.Ref] = cp.Status
	}
	return statusByRef
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
