package autosnap

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/spf13/cobra"
)

func newPendingCommand() *cobra.Command {
	var (
		allBranches bool
		branch      string
		explain     bool
	)

	cmd := &cobra.Command{
		Use:   "pending",
		Short: "List checkpoints after the latest checkpoint matching branch tip",
		Args:  cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := context.Background()
			repoRoot, _, branchRef, err := detectRepository(ctx)
			if err != nil {
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

			classifiedRefs, err := classifyPendingCheckpointRefs(ctx, repoRoot, refs, branch, allBranches)
			if err != nil {
				return err
			}

			if explain {
				explainRefs := classifiedCheckpointRefs(classifiedRefs)
				checkpoints, err := listCheckpointsFromRefs(ctx, repoRoot, explainRefs)
				if err != nil {
					return err
				}
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
			pending, err := listCheckpointsFromRefs(ctx, repoRoot, pendingRefs)
			if err != nil {
				return err
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
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&branch, "branch", "", "List pending checkpoints for a specific branch")
	cmd.Flags().BoolVar(&allBranches, "all", false, "List pending checkpoints for all branches")
	cmd.Flags().BoolVar(&explain, "explain", false, "Show integration status for all scanned checkpoints")
	return cmd
}

func filterPendingCheckpointRefs(ctx context.Context, repoRoot string, checkpoints []checkpointRefInfo, branch string, allBranches bool) ([]checkpointRefInfo, error) {
	classified, err := classifyPendingCheckpointRefs(ctx, repoRoot, checkpoints, branch, allBranches)
	if err != nil {
		return nil, err
	}
	return actionableCheckpointRefs(classified), nil
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
		return nil, nil
	}

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
			pending, err := classifyPendingRefsForBranch(ctx, repoRoot, ref, refs, allBranches)
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
	classified, err := classifyPendingRefsForBranch(ctx, repoRoot, baseRef, checkpoints, allBranches)
	if err != nil {
		return nil, err
	}
	return actionableCheckpointRefs(classified), nil
}

func classifyPendingRefsForBranch(ctx context.Context, repoRoot, baseRef string, checkpoints []checkpointRefInfo, allBranches bool) ([]classifiedCheckpointRef, error) {
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

	branchTipTreeResult, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", branchTip+"^{tree}")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve branch tip tree %q: %w", resolvedRef, gitCommandError(err, branchTipTreeResult))
	}
	branchTipTree := strings.TrimSpace(branchTipTreeResult.Stdout)
	if branchTipTree == "" {
		return nil, fmt.Errorf("failed to resolve branch tip tree %q", resolvedRef)
	}

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
		treeLines = make([]string, len(checkpoints))
		for i, cp := range checkpoints {
			tree, treeErr := getCheckpointTree(ctx, repoRoot, cp.Commit)
			if treeErr != nil {
				return nil, treeErr
			}
			treeLines[i] = strings.TrimSpace(tree)
		}
	}

	classified := make([]classifiedCheckpointRef, 0, len(checkpoints))
	newestIntegratedIndex := -1
	for i, cp := range checkpoints {
		status := checkpointStatusPending
		if i < len(treeLines) && strings.TrimSpace(treeLines[i]) == branchTipTree {
			status = checkpointStatusExact
			newestIntegratedIndex = i
		} else {
			mergeStatus, unsupported, err := classifyCheckpointMergeStatus(ctx, repoRoot, branchTip, branchTipTree, cp)
			if err != nil {
				return nil, err
			}
			if unsupported {
				return classifyPendingRefsStrict(checkpoints, treeLines, branchTipTree), nil
			}
			status = mergeStatus
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

	return classified, nil
}

func classifyCheckpointMergeStatus(ctx context.Context, repoRoot, branchTip, branchTipTree string, cp checkpointRefInfo) (checkpointPendingStatus, bool, error) {
	result, err := runGitCommand(ctx, repoRoot, nil, "merge-tree", "--write-tree", branchTip, cp.Commit)
	if err != nil && isMergeTreeWriteTreeUnsupported(result) {
		return checkpointStatusStrict, true, nil
	}

	firstLine := firstNonEmptyLine(result.Stdout)
	if firstLine == "" {
		firstLine = firstNonEmptyLine(result.Stderr)
	}
	if err == nil {
		if firstLine == branchTipTree {
			return checkpointStatusIntegrated, false, nil
		}
		return checkpointStatusPending, false, nil
	}
	if firstLine != "" && looksLikeObjectID(firstLine) {
		return checkpointStatusConflict, false, nil
	}
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
