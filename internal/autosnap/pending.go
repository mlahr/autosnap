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

			pendingRefs, err := filterPendingCheckpointRefs(ctx, repoRoot, refs, branch, allBranches)
			if err != nil {
				return err
			}

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
	return cmd
}

func filterPendingCheckpointRefs(ctx context.Context, repoRoot string, checkpoints []checkpointRefInfo, branch string, allBranches bool) ([]checkpointRefInfo, error) {
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
		checkrefs []checkpointRefInfo
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
			pending, err := filterPendingRefsForBranch(ctx, repoRoot, ref, refs, allBranches)
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

	pending := make([]checkpointRefInfo, 0, len(checkpoints))
	for _, result := range pendingByBranch {
		pending = append(pending, result.checkrefs...)
	}

	return pending, nil
}

func filterPendingRefsForBranch(ctx context.Context, repoRoot, baseRef string, checkpoints []checkpointRefInfo, allBranches bool) ([]checkpointRefInfo, error) {
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

	return checkpoints[syncedIndex+1:], nil
}
