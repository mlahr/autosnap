package autosnap

import (
	"context"
	"fmt"
	"io"
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
	)

	cmd := &cobra.Command{
		Use:   "pending",
		Short: "List checkpoints after the latest checkpoint matching branch tip",
		Args:  cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			debugLog := newPendingDebugLogger(cmd.ErrOrStderr(), debug)
			ctx := context.Background()
			debugLog.Printf("detecting repository")
			repoRoot, _, branchRef, err := detectRepository(ctx)
			if err != nil {
				return err
			}
			debugLog.Printf("repository detected root=%s current_branch=%s", repoRoot, branchRef)

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

			debugLog.Printf("classifying checkpoints count=%d", len(refs))
			classifiedRefs, err := classifyPendingCheckpointRefsDebug(ctx, repoRoot, refs, branch, allBranches, debugLog)
			if err != nil {
				return err
			}
			debugLog.Printf("classified checkpoints count=%d actionable=%d", len(classifiedRefs), len(actionableCheckpointRefs(classifiedRefs)))

			if explain {
				explainRefs := classifiedCheckpointRefs(classifiedRefs)
				debugLog.Printf("loading checkpoint metadata count=%d mode=explain", len(explainRefs))
				checkpoints, err := listCheckpointsFromRefs(ctx, repoRoot, explainRefs)
				if err != nil {
					return err
				}
				debugLog.Printf("loaded checkpoint metadata count=%d mode=explain", len(checkpoints))
				statusByRef := checkpointStatusByRef(classifiedRefs)
				if len(checkpoints) == 0 {
					if allBranches {
						fmt.Fprintln(out, "no checkpoints")
					} else if strings.TrimSpace(branch) != "" {
						fmt.Fprintf(out, "no checkpoints for branch %s\n", strings.TrimSpace(branch))
					} else {
						fmt.Fprintln(out, "no checkpoints for current branch")
					}
					return nil
				}
				for _, cp := range checkpoints {
					displayTimestamp := formatCheckpointTimestampForList(cp.Timestamp)
					status := string(statusByRef[cp.Ref])
					if status == "" {
						status = string(checkpointStatusPending)
					}
					if allBranches {
						fmt.Fprintf(out, "%s %s %s %s %s %s\n", cp.Branch, displayTimestamp, status, cp.Ref, cp.Commit, cp.Summary)
					} else {
						fmt.Fprintf(out, "%s %s %s %s %s\n", displayTimestamp, status, cp.Ref, cp.Commit, cp.Summary)
					}
				}
				return nil
			}

			pendingRefs := actionableCheckpointRefs(classifiedRefs)
			debugLog.Printf("loading checkpoint metadata count=%d mode=pending", len(pendingRefs))
			pending, err := listCheckpointsFromRefs(ctx, repoRoot, pendingRefs)
			if err != nil {
				return err
			}
			debugLog.Printf("loaded checkpoint metadata count=%d mode=pending", len(pending))

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
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&branch, "branch", "", "List pending checkpoints for a specific branch")
	cmd.Flags().BoolVar(&allBranches, "all", false, "List pending checkpoints for all branches")
	cmd.Flags().BoolVar(&debug, "debug", false, "Show progress diagnostics on stderr")
	cmd.Flags().BoolVar(&explain, "explain", false, "Show integration status for all scanned checkpoints")
	return cmd
}

func filterPendingCheckpointRefs(ctx context.Context, repoRoot string, checkpoints []checkpointRefInfo, branch string, allBranches bool) ([]checkpointRefInfo, error) {
	classified, err := classifyPendingCheckpointRefsDebug(ctx, repoRoot, checkpoints, branch, allBranches, pendingDebugLogger{})
	if err != nil {
		return nil, err
	}
	return actionableCheckpointRefs(classified), nil
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

func classifyPendingCheckpointRefs(ctx context.Context, repoRoot string, checkpoints []checkpointRefInfo, branch string, allBranches bool) ([]classifiedCheckpointRef, error) {
	return classifyPendingCheckpointRefsDebug(ctx, repoRoot, checkpoints, branch, allBranches, pendingDebugLogger{})
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
	classified, err := classifyPendingRefsForBranchDebug(ctx, repoRoot, baseRef, checkpoints, allBranches, pendingDebugLogger{})
	if err != nil {
		return nil, err
	}
	return actionableCheckpointRefs(classified), nil
}

func classifyPendingRefsForBranch(ctx context.Context, repoRoot, baseRef string, checkpoints []checkpointRefInfo, allBranches bool) ([]classifiedCheckpointRef, error) {
	return classifyPendingRefsForBranchDebug(ctx, repoRoot, baseRef, checkpoints, allBranches, pendingDebugLogger{})
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
		revParseArgs = append(revParseArgs, cp.Commit+"^{tree}")
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
			tree, treeErr := getCheckpointTree(ctx, repoRoot, cp.Commit)
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

func classifyCheckpointMergeStatus(ctx context.Context, repoRoot, branchTip, branchTipTree string, cp checkpointRefInfo) (checkpointPendingStatus, bool, error) {
	return classifyCheckpointMergeStatusDebug(ctx, repoRoot, branchTip, branchTipTree, cp, pendingDebugLogger{})
}

func classifyCheckpointMergeStatusDebug(ctx context.Context, repoRoot, branchTip, branchTipTree string, cp checkpointRefInfo, debugLog pendingDebugLogger) (checkpointPendingStatus, bool, error) {
	start := time.Now()
	result, err := runGitCommand(ctx, repoRoot, nil, "merge-tree", "--write-tree", branchTip, cp.Commit)
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
