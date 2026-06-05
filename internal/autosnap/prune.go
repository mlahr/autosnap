package autosnap

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newPruneCommand() *cobra.Command {
	var (
		currentBranch bool
		allBranches   bool
		branch        string
		keep          int
		olderThan     string
		apply         bool
	)

	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Prune old autosnap checkpoints",
		Args:  cobra.ExactArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			ctx := context.Background()
			repoRoot, _, branchRef, err := detectRepository(ctx)
			if err != nil {
				return err
			}

			currentBranchChanged := cmd.Flags().Changed("current-branch")
			scopeCount := 0
			if currentBranchChanged {
				scopeCount++
			}
			if strings.TrimSpace(branch) != "" {
				scopeCount++
			}
			if allBranches {
				scopeCount++
			}
			if scopeCount > 1 {
				return fmt.Errorf("prune accepts at most one scope flag: --current-branch, --branch, or --all-branches")
			}

			keepChanged := cmd.Flags().Changed("keep")
			olderThanChanged := cmd.Flags().Changed("older-than")
			policyCount := 0
			if keepChanged {
				policyCount++
			}
			if olderThanChanged && strings.TrimSpace(olderThan) != "" {
				policyCount++
			}
			if policyCount != 1 {
				return fmt.Errorf("prune requires exactly one retention policy: --keep or --older-than")
			}

			var refs []checkpointRefInfo
			switch {
			case currentBranchChanged || scopeCount == 0:
				refs, err = listCheckpointRefsForBranch(ctx, repoRoot, branchRef)
			case strings.TrimSpace(branch) != "":
				refs, err = listCheckpointRefsForBranch(ctx, repoRoot, strings.TrimSpace(branch))
			case allBranches:
				refs, err = listCheckpointRefsForAllBranches(ctx, repoRoot)
			}
			if err != nil {
				return err
			}

			var selected []checkpointRefInfo
			if keepChanged {
				selected, err = checkpointsPrunedByKeep(refs, keep)
			} else {
				duration, parseErr := parsePruneDuration(olderThan)
				if parseErr != nil {
					return parseErr
				}
				selected, err = checkpointsPrunedByAge(refs, time.Now().UTC().Add(-duration))
			}
			if err != nil {
				return err
			}

			sortCheckpointRefsForOutput(selected)
			if len(selected) == 0 {
				fmt.Fprintln(out, "no checkpoints matched prune policy")
				return nil
			}

			rows := pruneDisplayRows(ctx, repoRoot, selected)
			if apply {
				for _, ref := range selected {
					if _, err := runGitCommand(ctx, repoRoot, nil, "update-ref", "-d", ref.Ref); err != nil {
						return fmt.Errorf("failed to delete checkpoint ref %q: %w", ref.Ref, err)
					}
				}
				fmt.Fprintf(out, "pruned %d checkpoint(s)\n", len(selected))
			} else {
				fmt.Fprintf(out, "dry run: %d checkpoint(s) would be pruned\n", len(selected))
			}

			for _, row := range rows {
				fmt.Fprintf(out, "%s %s %s %s\n", row.Timestamp, row.Commit, row.Ref, row.Summary)
			}
			return nil
		},
	}

	keep = -1
	cmd.Flags().BoolVar(&currentBranch, "current-branch", false, "Prune checkpoints for the current branch")
	cmd.Flags().StringVar(&branch, "branch", "", "Prune checkpoints for a specific branch")
	cmd.Flags().BoolVar(&allBranches, "all-branches", false, "Prune checkpoints for all branches")
	cmd.Flags().IntVar(&keep, "keep", -1, "Keep the newest N checkpoints per branch")
	cmd.Flags().StringVar(&olderThan, "older-than", "", "Prune checkpoints older than a duration such as 24h or 7d")
	cmd.Flags().BoolVar(&apply, "apply", false, "Delete matching checkpoint refs")
	return cmd
}

type pruneDisplayRow struct {
	Ref       string
	Commit    string
	Timestamp string
	Summary   string
}

func pruneDisplayRows(ctx context.Context, repoRoot string, refs []checkpointRefInfo) []pruneDisplayRow {
	rows := make([]pruneDisplayRow, 0, len(refs))
	for _, ref := range refs {
		summary := "unknown"
		if message, err := getCommitMessage(ctx, repoRoot, ref.Ref); err == nil {
			summary = checkpointListSummary(message)
		}
		rows = append(rows, pruneDisplayRow{
			Ref:       ref.Ref,
			Commit:    ref.Commit,
			Timestamp: ref.Timestamp,
			Summary:   summary,
		})
	}
	return rows
}

func checkpointsPrunedByKeep(refs []checkpointRefInfo, keep int) ([]checkpointRefInfo, error) {
	if keep < 0 {
		return nil, fmt.Errorf("--keep must be non-negative")
	}

	byBranch := map[string][]checkpointRefInfo{}
	for _, ref := range refs {
		byBranch[ref.Branch] = append(byBranch[ref.Branch], ref)
	}

	var selected []checkpointRefInfo
	for _, branchRefs := range byBranch {
		sort.Slice(branchRefs, func(i, j int) bool {
			return compareCheckpointRefsNewestFirst(branchRefs[i], branchRefs[j])
		})
		if len(branchRefs) <= keep {
			continue
		}
		selected = append(selected, branchRefs[keep:]...)
	}

	return selected, nil
}

func checkpointsPrunedByAge(refs []checkpointRefInfo, cutoff time.Time) ([]checkpointRefInfo, error) {
	var selected []checkpointRefInfo
	for _, ref := range refs {
		ts, err := parseCheckpointTimestamp(ref.Timestamp)
		if err != nil {
			return nil, err
		}
		if ts.Before(cutoff) {
			selected = append(selected, ref)
		}
	}
	return selected, nil
}

func parsePruneDuration(value string) (time.Duration, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, fmt.Errorf("--older-than requires a duration")
	}

	if strings.HasSuffix(trimmed, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(trimmed, "d"))
		if err != nil || days < 0 {
			return 0, fmt.Errorf("invalid --older-than value %q", value)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}

	duration, err := time.ParseDuration(trimmed)
	if err != nil || duration < 0 {
		return 0, fmt.Errorf("invalid --older-than value %q", value)
	}
	return duration, nil
}

func parseCheckpointTimestamp(timestamp string) (time.Time, error) {
	timestamp = checkpointRefTimestamp(timestamp)
	parsed, err := time.Parse("20060102T150405Z", timestamp)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid checkpoint timestamp %q", timestamp)
	}
	return parsed.UTC(), nil
}

func sortCheckpointRefsForOutput(refs []checkpointRefInfo) {
	sort.Slice(refs, func(i, j int) bool {
		leftTime, leftErr := parseCheckpointTimestamp(refs[i].Timestamp)
		rightTime, rightErr := parseCheckpointTimestamp(refs[j].Timestamp)
		if leftErr == nil && rightErr == nil && !leftTime.Equal(rightTime) {
			return leftTime.Before(rightTime)
		}
		if refs[i].Timestamp != refs[j].Timestamp {
			return refs[i].Timestamp < refs[j].Timestamp
		}
		return refs[i].Ref < refs[j].Ref
	})
}

func compareCheckpointRefsNewestFirst(left, right checkpointRefInfo) bool {
	leftTime, leftErr := parseCheckpointTimestamp(left.Timestamp)
	rightTime, rightErr := parseCheckpointTimestamp(right.Timestamp)
	if leftErr == nil && rightErr == nil && !leftTime.Equal(rightTime) {
		return leftTime.After(rightTime)
	}
	if left.Timestamp != right.Timestamp {
		return left.Timestamp > right.Timestamp
	}
	return path.Base(left.Ref) > path.Base(right.Ref)
}
