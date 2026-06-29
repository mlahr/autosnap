package autosnap

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

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

type branchClassification struct {
	resolvedRef   string
	branchTip     string
	branchTipTree string
	treeLines     []string
}

func resolveBranchClassification(ctx context.Context, repoRoot, baseRef string, checkpoints []checkpointRefInfo, allBranches bool, debugLog pendingDebugLogger) (*branchClassification, error) {
	if len(checkpoints) == 0 {
		return nil, nil
	}

	resolvedRef := baseRef
	if strings.HasPrefix(resolvedRef, "detached-") {
		resolvedRef = "HEAD"
	}

	result, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", resolvedRef)
	if err != nil {
		if allBranches {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to resolve branch tip %q: %w", resolvedRef, gitCommandError(err, result))
	}
	branchTip := strings.TrimSpace(result.Stdout)
	if branchTip == "" {
		if allBranches {
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

	return &branchClassification{
		resolvedRef:   resolvedRef,
		branchTip:     branchTip,
		branchTipTree: branchTipTree,
		treeLines:     treeLines,
	}, nil
}

func classifyPendingRefsForBranchStreamDebug(ctx context.Context, repoRoot, baseRef string, checkpoints []checkpointRefInfo, allBranches bool, debugLog pendingDebugLogger, emit func(classifiedCheckpointRef)) error {
	if len(checkpoints) == 0 {
		return nil
	}
	debugLog.Printf("branch classification started branch=%s checkpoints=%d order=newest-first mode=explain", baseRef, len(checkpoints))
	branchStart := time.Now()

	bc, err := resolveBranchClassification(ctx, repoRoot, baseRef, checkpoints, allBranches, debugLog)
	if err != nil {
		return err
	}
	if bc == nil {
		return nil
	}
	checkpoints = checkpointRefsWithTrees(checkpoints, bc.treeLines)

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
		if i < len(bc.treeLines) && strings.TrimSpace(bc.treeLines[i]) == bc.branchTipTree {
			debugLog.Printf("checkpoint classified branch=%s index=%d/%d ref=%s commit=%s status=%s reason=tree_exact", baseRef, i+1, len(checkpoints), cp.Ref, cp.Commit, checkpointStatusExact)
			emitAt(i, checkpointStatusExact)
			for older := 0; older < i; older++ {
				status := checkpointStatusObsolete
				if older < len(bc.treeLines) && strings.TrimSpace(bc.treeLines[older]) == bc.branchTipTree {
					status = checkpointStatusExact
				}
				debugLog.Printf("checkpoint classified branch=%s index=%d/%d ref=%s commit=%s status=%s", baseRef, older+1, len(checkpoints), checkpoints[older].Ref, checkpoints[older].Commit, status)
				emitAt(older, status)
			}
			debugLog.Printf("branch classification finished branch=%s classified=%d elapsed=%s", baseRef, len(checkpoints), time.Since(branchStart).Round(time.Millisecond))
			return nil
		}

		debugLog.Printf("merge classification started branch=%s index=%d/%d ref=%s commit=%s", baseRef, i+1, len(checkpoints), cp.Ref, cp.Commit)
		mergeStatus, unsupported, err := classifyCheckpointMergeStatusDebug(ctx, repoRoot, bc.branchTip, bc.branchTipTree, cp, debugLog)
		if err != nil {
			return err
		}
		if unsupported {
			debugLog.Printf("merge-tree --write-tree unsupported; using strict classification branch=%s", baseRef)
			for _, classified := range classifyPendingRefsStrict(checkpoints, bc.treeLines, bc.branchTipTree) {
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
				if older < len(bc.treeLines) && strings.TrimSpace(bc.treeLines[older]) == bc.branchTipTree {
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

	bc, err := resolveBranchClassification(ctx, repoRoot, baseRef, checkpoints, allBranches, debugLog)
	if err != nil {
		return nil, err
	}
	if bc == nil {
		return nil, nil
	}
	checkpoints = checkpointRefsWithTrees(checkpoints, bc.treeLines)

	classified := make([]classifiedCheckpointRef, 0, len(checkpoints))
	newestIntegratedIndex := -1
	for i, cp := range checkpoints {
		status := checkpointStatusPending
		if i < len(bc.treeLines) && strings.TrimSpace(bc.treeLines[i]) == bc.branchTipTree {
			status = checkpointStatusExact
			newestIntegratedIndex = i
			debugLog.Printf("checkpoint classified branch=%s index=%d/%d ref=%s commit=%s status=%s reason=tree_exact", baseRef, i+1, len(checkpoints), cp.Ref, cp.Commit, status)
		} else {
			debugLog.Printf("merge classification started branch=%s index=%d/%d ref=%s commit=%s", baseRef, i+1, len(checkpoints), cp.Ref, cp.Commit)
			mergeStatus, unsupported, err := classifyCheckpointMergeStatusDebug(ctx, repoRoot, bc.branchTip, bc.branchTipTree, cp, debugLog)
			if err != nil {
				return nil, err
			}
			if unsupported {
				debugLog.Printf("merge-tree --write-tree unsupported; using strict classification branch=%s", baseRef)
				return classifyPendingRefsStrict(checkpoints, bc.treeLines, bc.branchTipTree), nil
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

	bc, err := resolveBranchClassification(ctx, repoRoot, baseRef, checkpoints, allBranches, debugLog)
	if err != nil {
		return nil, err
	}
	if bc == nil {
		return nil, nil
	}
	checkpoints = checkpointRefsWithTrees(checkpoints, bc.treeLines)

	actionableByIndex := make([]bool, len(checkpoints))
	boundaryIndex := -1
	for i := len(checkpoints) - 1; i >= 0; i-- {
		cp := checkpoints[i]
		if i < len(bc.treeLines) && strings.TrimSpace(bc.treeLines[i]) == bc.branchTipTree {
			boundaryIndex = i
			debugLog.Printf("checkpoint classified branch=%s index=%d/%d ref=%s commit=%s status=%s reason=tree_exact", baseRef, i+1, len(checkpoints), cp.Ref, cp.Commit, checkpointStatusExact)
			debugLog.Printf("early stop branch=%s index=%d/%d status=%s older_checkpoints=%d", baseRef, i+1, len(checkpoints), checkpointStatusExact, i)
			break
		}

		debugLog.Printf("merge classification started branch=%s index=%d/%d ref=%s commit=%s", baseRef, i+1, len(checkpoints), cp.Ref, cp.Commit)
		mergeStatus, unsupported, err := classifyCheckpointMergeStatusDebug(ctx, repoRoot, bc.branchTip, bc.branchTipTree, cp, debugLog)
		if err != nil {
			return nil, err
		}
		if unsupported {
			debugLog.Printf("merge-tree --write-tree unsupported; using strict actionable classification branch=%s", baseRef)
			pending := actionablePendingRefsStrict(checkpoints, bc.treeLines, bc.branchTipTree)
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

func checkpointRefsWithTrees(checkpoints []checkpointRefInfo, treeLines []string) []checkpointRefInfo {
	if len(checkpoints) == 0 {
		return nil
	}
	withTrees := make([]checkpointRefInfo, len(checkpoints))
	copy(withTrees, checkpoints)
	for i := range withTrees {
		if i >= len(treeLines) {
			continue
		}
		withTrees[i].Tree = strings.TrimSpace(treeLines[i])
	}
	return withTrees
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
