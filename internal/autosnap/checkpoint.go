package autosnap

import (
	"context"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type checkpointInfo struct {
	Ref       string
	Timestamp string
	Commit    string
	Status    string
	CheckCmd  string
}

type checkpointRefInfo struct {
	Ref       string
	Commit    string
	Timestamp string
}

func snapshotRefPrefix(branch string) string {
	return path.Join("refs", "autosnapshots", branch)
}

func snapshotRef(branch string, timestamp string) string {
	return path.Join(snapshotRefPrefix(branch), timestamp)
}

func currentTimestamp() string {
	return time.Now().UTC().Format("20060102T150405Z")
}

func computeWorktreeTree(ctx context.Context, repoRoot, gitDirectory string) (string, error) {
	tmpIndex := fmt.Sprintf("%s/autosnap-index.%d", gitDirectory, time.Now().UnixNano())
	defer func() {
		_ = os.Remove(tmpIndex)
	}()

	env := map[string]string{
		"GIT_INDEX_FILE": tmpIndex,
	}

	if _, err := runGitCommand(ctx, repoRoot, env, "read-tree", "HEAD"); err != nil {
		return "", err
	}
	if _, err := runGitCommand(ctx, repoRoot, env, "add", "-A"); err != nil {
		return "", err
	}
	treeResult, err := runGitCommand(ctx, repoRoot, env, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(treeResult.Stdout), nil
}

func createCheckpoint(ctx context.Context, repoRoot, branchRef, checkCommand string, idle time.Duration, tree string) (string, string, error) {
	headResult, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	head := strings.TrimSpace(headResult.Stdout)

	ts := currentTimestamp()
	base := "unknown"
	if len(head) >= 7 {
		base = head[:7]
	}
	message := fmt.Sprintf(
		"autosnap: passing checkpoint %s branch: %s check: %s idle_seconds: %d base: %s",
		ts,
		branchRef,
		checkCommand,
		int(idle.Seconds()),
		base,
	)

	commitResult, err := runGitCommand(ctx, repoRoot, nil, "commit-tree", tree, "-p", head, "-m", message)
	if err != nil {
		return "", "", err
	}
	commit := strings.TrimSpace(commitResult.Stdout)

	ref := snapshotRef(branchRef, ts)
	if _, err := runGitCommand(ctx, repoRoot, nil, "update-ref", ref, commit); err != nil {
		return "", "", err
	}

	return ref, commit, nil
}

func getCheckpointTree(ctx context.Context, repoRoot, ref string) (string, error) {
	treeRef := fmt.Sprintf("%s^{tree}", ref)
	result, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", treeRef)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func getLatestCheckpointForBranch(ctx context.Context, repoRoot, branchRef string) (string, string, string, error) {
	refPrefix := snapshotRefPrefix(branchRef)
	result, err := runGitCommand(
		ctx,
		repoRoot,
		nil,
		"for-each-ref",
		"--sort=-refname",
		"--format=%(refname) %(objectname:short) %(creatordate:unix)",
		refPrefix+"/*",
	)
	if err != nil {
		return "", "", "", err
	}

	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return "", "", "", nil
	}

	parts := strings.Fields(lines[0])
	if len(parts) < 2 {
		return "", "", "", nil
	}

	ref := parts[0]
	commit := parts[1]
	ts := ""
	if len(parts) > 2 {
		if unixTs, parseErr := strconv.ParseInt(parts[2], 10, 64); parseErr == nil {
			ts = time.Unix(unixTs, 0).UTC().Format("2006-01-02 15:04:05")
		}
	}

	if ts == "" {
		ts = path.Base(ref)
	}

	return ref, ts, commit, nil
}

func listCheckpointRefsForBranch(ctx context.Context, repoRoot, branchRef string) ([]checkpointRefInfo, error) {
	refPrefix := snapshotRefPrefix(branchRef)
	result, err := runGitCommand(
		ctx,
		repoRoot,
		nil,
		"for-each-ref",
		"--format=%(refname) %(objectname:short)",
		refPrefix+"/*",
	)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return nil, nil
	}

	var refs []checkpointRefInfo
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		refName := parts[0]
		sha := parts[1]
		refs = append(refs, checkpointRefInfo{
			Ref:       refName,
			Commit:    sha,
			Timestamp: path.Base(refName),
		})
	}

	return refs, nil
}

func resolveCheckpointRefForArg(ctx context.Context, repoRoot, branchRef, arg string) (string, error) {
	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		return "", fmt.Errorf("checkpoint identifier is required")
	}

	if strings.HasPrefix(trimmed, "refs/") {
		if !strings.HasPrefix(trimmed, "refs/autosnapshots/") {
			return "", fmt.Errorf("not an autosnap checkpoint ref: %q", trimmed)
		}

		if _, err := resolveAutosnapRefToCommit(ctx, repoRoot, trimmed); err != nil {
			return "", fmt.Errorf("checkpoint not found: %q", trimmed)
		}

		return trimmed, nil
	}

	exactRef := snapshotRef(branchRef, trimmed)
	if _, err := resolveAutosnapRefToCommit(ctx, repoRoot, exactRef); err == nil {
		return exactRef, nil
	}

	entries, err := listCheckpointRefsForBranch(ctx, repoRoot, branchRef)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("no checkpoints for current branch")
	}

	exactMatches := filterCheckpointRefsByTimestamp(entries, trimmed)
	if len(exactMatches) == 1 {
		return exactMatches[0].Ref, nil
	}
	if len(exactMatches) > 1 {
		return "", fmt.Errorf("checkpoint identifier %q is ambiguous", trimmed)
	}

	if !isCommitLike(trimmed) {
		return "", fmt.Errorf("checkpoint not found: %q", trimmed)
	}

	exactCommitMatches := filterCheckpointRefsByCommit(entries, trimmed, false)
	if len(exactCommitMatches) == 1 {
		return exactCommitMatches[0].Ref, nil
	}
	if len(exactCommitMatches) > 1 {
		return "", fmt.Errorf("checkpoint identifier %q is ambiguous", trimmed)
	}

	prefixCommitMatches := filterCheckpointRefsByCommit(entries, trimmed, true)
	if len(prefixCommitMatches) == 1 {
		return prefixCommitMatches[0].Ref, nil
	}
	if len(prefixCommitMatches) > 1 {
		return "", fmt.Errorf("checkpoint identifier %q is ambiguous", trimmed)
	}

	return "", fmt.Errorf("checkpoint not found: %q", trimmed)
}

func resolveAutosnapRefToCommit(ctx context.Context, repoRoot, ref string) (string, error) {
	result, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", "--verify", ref)
	if err != nil {
		if result.ExitCode == 128 {
			return "", fmt.Errorf("ref %q does not exist", ref)
		}
		return "", err
	}

	commit := strings.TrimSpace(result.Stdout)
	if commit == "" {
		return "", fmt.Errorf("unable to resolve ref %q", ref)
	}

	return commit, nil
}

func resolveCheckpointRefMetadata(ctx context.Context, repoRoot, branchRef, arg string) (checkpointRefInfo, error) {
	ref, err := resolveCheckpointRefForArg(ctx, repoRoot, branchRef, arg)
	if err != nil {
		return checkpointRefInfo{}, err
	}

	if strings.HasPrefix(ref, "refs/autosnapshots/") {
		commit, err := resolveAutosnapRefToCommit(ctx, repoRoot, ref)
		if err != nil {
			return checkpointRefInfo{}, err
		}

		return checkpointRefInfo{
			Ref:       ref,
			Commit:    commit,
			Timestamp: path.Base(ref),
		}, nil
	}

	entries, err := listCheckpointRefsForBranch(ctx, repoRoot, branchRef)
	if err != nil {
		return checkpointRefInfo{}, err
	}

	for _, entry := range entries {
		if entry.Ref == ref {
			return entry, nil
		}
	}

	commit, err := resolveAutosnapRefToCommit(ctx, repoRoot, ref)
	if err != nil {
		return checkpointRefInfo{}, err
	}

	return checkpointRefInfo{
		Ref:       ref,
		Commit:    commit,
		Timestamp: path.Base(ref),
	}, nil
}

func isCommitLike(candidate string) bool {
	if len(candidate) < 4 {
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

func filterCheckpointRefsByTimestamp(entries []checkpointRefInfo, timestamp string) []checkpointRefInfo {
	var matches []checkpointRefInfo
	for _, entry := range entries {
		if entry.Timestamp == timestamp {
			matches = append(matches, entry)
		}
	}
	return matches
}

func filterCheckpointRefsByCommit(entries []checkpointRefInfo, commit string, prefix bool) []checkpointRefInfo {
	normalized := strings.ToLower(commit)
	var matches []checkpointRefInfo
	for _, entry := range entries {
		cand := strings.ToLower(entry.Commit)
		if cand == normalized {
			matches = append(matches, entry)
			if prefix {
				continue
			}
		}
		if prefix && strings.HasPrefix(cand, normalized) {
			matches = append(matches, entry)
		}
	}
	return matches
}

func parseCheckpointMessage(message string) (string, string) {
	line := strings.TrimSpace(strings.SplitN(message, "\n", 2)[0])
	if line == "" {
		return "unknown", ""
	}

	status, statusLine := parseCheckpointStatus(line)
	if !statusLine {
		return "unknown", parseCheckpointField(line, "check:")
	}

	return status, parseCheckpointField(line, "check:")
}

var checkpointStatusRegex = regexp.MustCompile(`\b(passing|failing)\b`)

func parseCheckpointStatus(line string) (string, bool) {
	checkIdx := strings.Index(line, "check:")
	target := line
	if checkIdx >= 0 {
		target = line[:checkIdx]
	}

	matches := checkpointStatusRegex.FindStringSubmatch(strings.ToLower(target))
	if len(matches) != 2 {
		return "unknown", false
	}

	return matches[1], true
}

func parseCheckpointField(line, key string) string {
	idx := strings.Index(line, key)
	if idx < 0 {
		return ""
	}

	rest := line[idx+len(key):]
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return ""
	}

	end := len(rest)
	for _, marker := range []string{" idle_seconds:", " base:", " branch:"} {
		if next := strings.Index(rest, marker); next >= 0 && next < end {
			end = next
		}
	}
	return strings.TrimSpace(rest[:end])
}

func getCommitMessage(ctx context.Context, repoRoot, ref string) (string, error) {
	result, err := runGitCommand(ctx, repoRoot, nil, "log", "-1", "--pretty=%B", ref)
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

func listCheckpointsForBranch(ctx context.Context, repoRoot, branchRef string) ([]checkpointInfo, error) {
	entries, err := listCheckpointRefsForBranch(ctx, repoRoot, branchRef)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}

	checkpoints := make([]checkpointInfo, 0, len(entries))
	for _, entry := range entries {
		checkCmd := ""
		status := "unknown"

		msg, err := getCommitMessage(ctx, repoRoot, entry.Ref)
		if err == nil {
			status, checkCmd = parseCheckpointMessage(msg)
		}

		checkpoints = append(checkpoints, checkpointInfo{
			Ref:       entry.Ref,
			Commit:    entry.Commit,
			Timestamp: entry.Timestamp,
			Status:    status,
			CheckCmd:  checkCmd,
		})
	}

	sort.Slice(checkpoints, func(i, j int) bool {
		return checkpoints[i].Timestamp > checkpoints[j].Timestamp
	})

	return checkpoints, nil
}
