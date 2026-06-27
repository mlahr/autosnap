package autosnap

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

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
