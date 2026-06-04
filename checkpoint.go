package main

import (
	"context"
	"fmt"
	"os"
	"path"
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
		"--format=%(refname:short) %(objectname:short) %(creatordate:unix)",
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

func parseCheckpointMessage(message string) (string, string) {
	line := strings.TrimSpace(strings.SplitN(message, "\n", 2)[0])
	if line == "" {
		return "unknown", ""
	}

	status := "unknown"
	if strings.Contains(line, "passing") {
		status = "passing"
	}
	if strings.Contains(line, "failing") {
		status = "failing"
	}

	checkCmd := ""
	if idx := strings.Index(line, "check:"); idx >= 0 {
		cmd := line[idx+len("check:"):]
		if end := strings.Index(cmd, " idle_seconds:"); end >= 0 {
			checkCmd = strings.TrimSpace(cmd[:end])
		} else {
			checkCmd = strings.TrimSpace(cmd)
		}
	}

	return status, checkCmd
}

func getCommitMessage(ctx context.Context, repoRoot, ref string) (string, error) {
	result, err := runGitCommand(ctx, repoRoot, nil, "log", "-1", "--pretty=%B", ref)
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

func listCheckpointsForBranch(ctx context.Context, repoRoot, branchRef string) ([]checkpointInfo, error) {
	refPrefix := snapshotRefPrefix(branchRef)
	result, err := runGitCommand(ctx, repoRoot, nil, "for-each-ref", "--sort=-refname", "--format=%(refname:short) %(objectname:short)", refPrefix+"/*")
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return nil, nil
	}

	var checkpoints []checkpointInfo
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		refName := parts[0]
		sha := parts[1]
		timestamp := path.Base(refName)
		checkCmd := ""
		status := "unknown"

		msg, err := getCommitMessage(ctx, repoRoot, refName)
		if err == nil {
			status, checkCmd = parseCheckpointMessage(msg)
		}

		checkpoints = append(checkpoints, checkpointInfo{
			Ref:       refName,
			Commit:    sha,
			Timestamp: timestamp,
			Status:    status,
			CheckCmd:  checkCmd,
		})
	}

	sort.Slice(checkpoints, func(i, j int) bool {
		return checkpoints[i].Timestamp > checkpoints[j].Timestamp
	})

	return checkpoints, nil
}
