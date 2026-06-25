package autosnap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	maxCheckpointRefCollisionRetries = 20
	snapshotModeBoth                 = "both"
	snapshotModeStaged               = "staged"
	snapshotModeWorking              = "working"
)

const (
	commitModeCheckpoint = "checkpoint"
	commitModeDirect     = "direct"
	commitModeSync       = "sync"
)

const (
	conflictPolicyManual     = "manual"
	conflictPolicyBase       = "base"
	conflictPolicyCheckpoint = "checkpoint"
	conflictPolicyHead       = "head"
)

type checkpointInfo struct {
	Ref       string
	Timestamp string
	Commit    string
	Branch    string
	Status    string
	CheckCmd  string
	Summary   string
}

type checkpointRefInfo struct {
	Ref        string
	Commit     string
	FullCommit string
	Timestamp  string
	Branch     string
}

type checkpointPatchRange struct {
	Start checkpointRefInfo
	End   checkpointRefInfo
	Base  string
}

type checkpointWorktreeMatch string

const (
	checkpointWorktreeMatchWorktree checkpointWorktreeMatch = "worktree"
	checkpointWorktreeMatchIndex    checkpointWorktreeMatch = "index"
)

type patchConflictError struct {
	action     string
	checkpoint string
	paths      []string
	detail     string
}

func (e patchConflictError) Error() string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "checkpoint %s with conflicts", e.action)
	if e.checkpoint != "" {
		fmt.Fprintf(&builder, ": %s", e.checkpoint)
	}
	if len(e.paths) > 0 {
		builder.WriteString("\n\nConflicted paths:")
		for _, path := range e.paths {
			fmt.Fprintf(&builder, "\n  %s", path)
		}
	}
	builder.WriteString("\n\nGit applied the checkpoint patch, but conflicts remain in your worktree.")
	builder.WriteString("\n\nResolve the conflicts manually or with your configured merge tool:")
	builder.WriteString("\n  git status")
	builder.WriteString("\n  git mergetool")
	builder.WriteString("\n  git add <resolved-files>")
	builder.WriteString("\n  git commit -m \"...\"")
	builder.WriteString("\n\nTo abandon these conflicted changes:")
	builder.WriteString("\n  git reset --hard")
	if e.detail != "" {
		builder.WriteString("\n\nGit output:")
		for _, line := range strings.Split(e.detail, "\n") {
			fmt.Fprintf(&builder, "\n  %s", line)
		}
	}
	return builder.String()
}

type gitPosition struct {
	BranchRef string
	Head      string
}

func snapshotRefPrefix(branch string) string {
	return path.Join("refs", "autosnapshots", branch)
}

func snapshotRef(branch string, timestamp string) string {
	return path.Join(snapshotRefPrefix(branch), timestamp)
}

var currentTimestampFn = func() string {
	return time.Now().UTC().Format("20060102T150405Z")
}

func currentTimestamp() string {
	return currentTimestampFn()
}

var zeroObjectID = strings.Repeat("0", 40)

func formatCheckpointTimestamp(timestamp string) string {
	parsed, err := time.Parse("20060102T150405Z", checkpointRefTimestamp(timestamp))
	if err == nil {
		return parsed.Local().Format("2006-01-02 15:04:05 MST")
	}

	parsedLegacy, err := time.Parse("2006-01-02 15:04:05", timestamp)
	if err == nil {
		return parsedLegacy.Local().Format("2006-01-02 15:04:05 MST")
	}

	return timestamp
}

func checkpointRefTimestamp(ref string) string {
	leaf := path.Base(ref)
	if idx := strings.Index(leaf, "."); idx != -1 {
		return leaf[:idx]
	}
	return leaf
}

func checkpointRefForAttempt(branchRef, timestamp, commit string, attempt int) string {
	leaf := timestamp
	if attempt > 0 {
		shortCommit := commit
		if len(shortCommit) > 7 {
			shortCommit = shortCommit[:7]
		}
		leaf = leaf + "." + shortCommit
		if attempt > 1 {
			leaf = leaf + "." + strconv.Itoa(attempt)
		}
	}
	return snapshotRef(branchRef, leaf)
}

func isCheckpointRefCollisionErr(result commandResult, err error) bool {
	return err != nil &&
		result.ExitCode == 128 &&
		strings.Contains(strings.ToLower(result.Stderr), "reference already exists")
}

func createUniqueCheckpointRef(ctx context.Context, repoRoot, branchRef, timestamp, commit string) (string, error) {
	maxAttempts := maxCheckpointRefCollisionRetries + 1
	for attempt := 0; attempt < maxAttempts; attempt++ {
		ref := checkpointRefForAttempt(branchRef, timestamp, commit, attempt)
		result, err := runGitCommand(ctx, repoRoot, nil, "update-ref", ref, commit, zeroObjectID)
		if err == nil {
			return ref, nil
		}
		if isCheckpointRefCollisionErr(result, err) {
			if attempt < maxAttempts-1 {
				continue
			}
			return "", fmt.Errorf("failed to allocate unique checkpoint ref for timestamp %s after %d attempts", timestamp, maxAttempts)
		}
		return "", gitCommandError(err, result)
	}
	return "", fmt.Errorf("failed to allocate unique checkpoint ref for timestamp %s after %d attempts", timestamp, maxAttempts)
}

func currentGitPosition(ctx context.Context, repoRoot string) (gitPosition, error) {
	branchResult, err := runGitCommand(ctx, repoRoot, nil, "branch", "--show-current")
	if err != nil {
		return gitPosition{}, err
	}

	headResult, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", "HEAD")
	if err != nil {
		return gitPosition{}, err
	}
	head := strings.TrimSpace(headResult.Stdout)
	if head == "" {
		return gitPosition{}, fmt.Errorf("unable to resolve HEAD")
	}

	branchRef := strings.TrimSpace(branchResult.Stdout)
	if branchRef == "" {
		shortHead := head
		if len(shortHead) > 7 {
			shortHead = shortHead[:7]
		}
		branchRef = "detached-" + shortHead
	}

	return gitPosition{
		BranchRef: branchRef,
		Head:      head,
	}, nil
}

func computeWorktreeTree(ctx context.Context, repoRoot, gitDirectory, mode string) (string, error) {
	mode, err := normalizeSnapshotMode(mode)
	if err != nil {
		return "", err
	}

	tmpIndex := fmt.Sprintf("%s/autosnap-index.%d", gitDirectory, time.Now().UnixNano())
	defer func() {
		_ = os.Remove(tmpIndex)
	}()

	env := map[string]string{
		"GIT_INDEX_FILE": tmpIndex,
	}

	switch mode {
	case snapshotModeBoth, snapshotModeWorking:
		if _, err := runGitCommand(ctx, repoRoot, env, "read-tree", "HEAD"); err != nil {
			return "", err
		}
		if _, err := runGitCommand(ctx, repoRoot, env, "add", "-A"); err != nil {
			return "", err
		}
	case snapshotModeStaged:
		if _, err := os.Stat(filepath.Join(gitDirectory, "index")); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return "", fmt.Errorf("git index file not found for staged snapshot mode")
			}
			return "", err
		}
		if err := copyFile(filepath.Join(gitDirectory, "index"), tmpIndex); err != nil {
			return "", err
		}
	}

	treeResult, err := runGitCommand(ctx, repoRoot, env, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(treeResult.Stdout), nil
}

func copyFile(src, dest string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}
	return dstFile.Sync()
}

func normalizeSnapshotMode(mode string) (string, error) {
	if mode == "" {
		return snapshotModeBoth, nil
	}
	switch mode {
	case snapshotModeBoth, snapshotModeStaged, snapshotModeWorking:
		return mode, nil
	}
	return "", fmt.Errorf("invalid snapshot mode %q (expected both, staged, working)", mode)
}

func normalizeCommitMode(mode string) (string, error) {
	if mode == "" {
		return commitModeCheckpoint, nil
	}
	switch mode {
	case commitModeCheckpoint, commitModeDirect, commitModeSync:
		return mode, nil
	}
	return "", fmt.Errorf("invalid commit mode %q (expected checkpoint, direct, sync)", mode)
}

func isDirectCommitMode(mode string) bool {
	return mode == commitModeDirect || mode == commitModeSync
}

func createCheckpoint(ctx context.Context, repoRoot, branchRef, checkCommand string, idle time.Duration, tree string, commitMessage string) (string, string, error) {
	return createCheckpointChecked(ctx, repoRoot, branchRef, "", checkCommand, idle, tree, commitMessage)
}

func createCheckpointChecked(ctx context.Context, repoRoot, expectedBranchRef, expectedHead, checkCommand string, idle time.Duration, tree string, commitMessage string) (string, string, error) {
	position, err := currentGitPosition(ctx, repoRoot)
	if err != nil {
		return "", "", err
	}
	if expectedBranchRef != "" && position.BranchRef != expectedBranchRef {
		return "", "", fmt.Errorf("git branch changed during checkpoint: was %s, now %s", expectedBranchRef, position.BranchRef)
	}
	if expectedHead != "" && position.Head != expectedHead {
		return "", "", fmt.Errorf("git HEAD changed during checkpoint: was %s, now %s", expectedHead, position.Head)
	}

	headTree, err := getCheckpointTree(ctx, repoRoot, "HEAD")
	if err != nil {
		return "", "", err
	}
	if headTree == tree {
		return "", "", nil
	}

	ts := currentTimestamp()
	message := autosnapCommitMessage(commitMessage, ts, position, checkCommand, idle)

	commit, err := createCommitFromTree(ctx, repoRoot, tree, position.Head, message)
	if err != nil {
		return "", "", err
	}

	ref, err := createUniqueCheckpointRef(ctx, repoRoot, position.BranchRef, ts, commit)
	if err != nil {
		return "", "", err
	}

	return ref, commit, nil
}

func createDirectCommitChecked(ctx context.Context, repoRoot, expectedBranchRef, expectedHead, checkCommand string, idle time.Duration, tree string, commitMessage string) (string, bool, string, error) {
	branchResult, err := runGitCommand(ctx, repoRoot, nil, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || strings.TrimSpace(branchResult.Stdout) == "" {
		return "", false, "", fmt.Errorf("direct commit mode requires a checked-out branch")
	}

	position, err := currentGitPosition(ctx, repoRoot)
	if err != nil {
		return "", false, "", err
	}
	if expectedBranchRef != "" && position.BranchRef != expectedBranchRef {
		return "", false, "", fmt.Errorf("git branch changed during direct commit: was %s, now %s", expectedBranchRef, position.BranchRef)
	}
	if expectedHead != "" && position.Head != expectedHead {
		return "", false, "", fmt.Errorf("git HEAD changed during direct commit: was %s, now %s", expectedHead, position.Head)
	}

	headTree, err := getCheckpointTree(ctx, repoRoot, "HEAD")
	if err != nil {
		return "", false, "", err
	}
	if headTree == tree {
		return position.Head, false, "", nil
	}

	ts := currentTimestamp()
	message := autosnapCommitMessage(commitMessage, ts, position, checkCommand, idle)
	commit, err := createCommitFromTree(ctx, repoRoot, tree, position.Head, message)
	if err != nil {
		return "", false, "", err
	}
	if commit == "" {
		return "", false, "", fmt.Errorf("direct commit did not create a commit")
	}

	if _, err := runGitCommand(ctx, repoRoot, nil, "reset", "--hard", commit); err != nil {
		return "", false, "", err
	}

	return commit, true, ts, nil
}

func syncDirectCommit(ctx context.Context, repoRoot string) (string, error) {
	pullResult, err := runGitCommand(ctx, repoRoot, nil, "pull", "--rebase")
	if err != nil {
		abortErr := abortRebaseIfActive(ctx, repoRoot)
		syncErr := fmt.Errorf("git pull --rebase failed: %w", gitCommandError(err, pullResult))
		if abortErr != nil {
			syncErr = fmt.Errorf("%w; rebase abort failed: %v", syncErr, abortErr)
		}
		return currentHeadOrEmpty(ctx, repoRoot), syncErr
	}

	if status, err := worktreeStatus(ctx, repoRoot); err != nil {
		return currentHeadOrEmpty(ctx, repoRoot), fmt.Errorf("git status after pull --rebase failed: %w", err)
	} else if status != "" {
		return currentHeadOrEmpty(ctx, repoRoot), fmt.Errorf("worktree not clean after pull --rebase:\n%s", status)
	}

	pushResult, err := runGitCommand(ctx, repoRoot, nil, "push")
	if err != nil {
		return currentHeadOrEmpty(ctx, repoRoot), fmt.Errorf("git push failed: %w", gitCommandError(err, pushResult))
	}

	return currentHeadOrEmpty(ctx, repoRoot), nil
}

func abortRebaseIfActive(ctx context.Context, repoRoot string) error {
	gitDirectory, err := gitDir(ctx, repoRoot)
	if err != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(gitDirectory, "rebase-merge")); err == nil {
		return abortRebase(ctx, repoRoot)
	}
	if _, err := os.Stat(filepath.Join(gitDirectory, "rebase-apply")); err == nil {
		return abortRebase(ctx, repoRoot)
	}
	return nil
}

func abortRebase(ctx context.Context, repoRoot string) error {
	result, err := runGitCommand(ctx, repoRoot, nil, "rebase", "--abort")
	if err != nil {
		return gitCommandError(err, result)
	}
	if status, err := worktreeStatus(ctx, repoRoot); err != nil {
		return fmt.Errorf("git status after rebase abort failed: %w", err)
	} else if status != "" {
		return fmt.Errorf("worktree not clean after rebase abort:\n%s", status)
	}
	return nil
}

func worktreeStatus(ctx context.Context, repoRoot string) (string, error) {
	result, err := runGitCommand(ctx, repoRoot, nil, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return "", gitCommandError(err, result)
	}
	return strings.TrimSpace(result.Stdout), nil
}

func currentHeadOrEmpty(ctx context.Context, repoRoot string) string {
	result, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}

func autosnapCommitMessage(commitMessage, timestamp string, position gitPosition, checkCommand string, idle time.Duration) string {
	base := "unknown"
	if len(position.Head) >= 7 {
		base = position.Head[:7]
	}
	message := strings.TrimSpace(commitMessage)
	if message == "" {
		message = fmt.Sprintf(
			"autosnap: passing checkpoint %s branch: %s check: %s idle_seconds: %d base: %s",
			timestamp,
			position.BranchRef,
			checkCommand,
			int(idle.Seconds()),
			base,
		)
	}
	return message
}

func createCommitFromTree(ctx context.Context, repoRoot, tree, parent, message string) (string, error) {
	commitResult, err := runGitCommandWithInput(ctx, repoRoot, nil, message, "commit-tree", tree, "-p", parent, "-F", "-")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(commitResult.Stdout), nil
}

func getCheckpointTree(ctx context.Context, repoRoot, ref string) (string, error) {
	treeRef := fmt.Sprintf("%s^{tree}", ref)
	result, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", treeRef)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func checkpointWorktreeMatches(ctx context.Context, repoRoot string, checkpoints []checkpointInfo) (map[string]checkpointWorktreeMatch, error) {
	matches := map[string]checkpointWorktreeMatch{}
	if len(checkpoints) == 0 {
		return matches, nil
	}

	gitDirectory, err := gitDir(ctx, repoRoot)
	if err != nil {
		return nil, err
	}
	worktreeTree, err := computeWorktreeTree(ctx, repoRoot, gitDirectory, snapshotModeBoth)
	if err != nil {
		return nil, err
	}
	indexTree, err := computeWorktreeTree(ctx, repoRoot, gitDirectory, snapshotModeStaged)
	if err != nil {
		return nil, err
	}

	treeLines, err := checkpointTreeLines(ctx, repoRoot, checkpoints)
	if err != nil {
		return nil, err
	}
	for i, cp := range checkpoints {
		if i >= len(treeLines) {
			continue
		}
		tree := strings.TrimSpace(treeLines[i])
		switch {
		case tree != "" && tree == worktreeTree:
			matches[cp.Ref] = checkpointWorktreeMatchWorktree
		case tree != "" && tree == indexTree:
			matches[cp.Ref] = checkpointWorktreeMatchIndex
		}
	}
	return matches, nil
}

func checkpointTreeLines(ctx context.Context, repoRoot string, checkpoints []checkpointInfo) ([]string, error) {
	if len(checkpoints) == 0 {
		return nil, nil
	}

	revParseArgs := make([]string, 0, len(checkpoints)+1)
	revParseArgs = append(revParseArgs, "rev-parse")
	for _, cp := range checkpoints {
		revParseArgs = append(revParseArgs, cp.Ref+"^{tree}")
	}
	treesResult, err := runGitCommand(ctx, repoRoot, nil, revParseArgs...)
	if err != nil {
		return nil, gitCommandError(err, treesResult)
	}

	treeLines := strings.Split(strings.TrimSpace(treesResult.Stdout), "\n")
	if len(treeLines) == len(checkpoints) {
		return treeLines, nil
	}

	treeLines = make([]string, len(checkpoints))
	for i, cp := range checkpoints {
		tree, treeErr := getCheckpointTree(ctx, repoRoot, cp.Ref)
		if treeErr != nil {
			return nil, treeErr
		}
		treeLines[i] = strings.TrimSpace(tree)
	}
	return treeLines, nil
}

func checkpointWorktreeMatchMarker(match checkpointWorktreeMatch) string {
	switch match {
	case checkpointWorktreeMatchWorktree:
		return "**"
	case checkpointWorktreeMatchIndex:
		return "* "
	default:
		return "  "
	}
}

func getCommitParent(ctx context.Context, repoRoot, commit string) (string, error) {
	result, err := runGitCommand(ctx, repoRoot, nil, "rev-list", "--parents", "-n", "1", commit)
	if err != nil {
		return "", err
	}

	fields := strings.Fields(result.Stdout)
	if len(fields) < 2 {
		return "", fmt.Errorf("checkpoint commit %q has no parent", commit)
	}

	return fields[1], nil
}

func ensureCleanWorktree(ctx context.Context, repoRoot, command string, force bool) error {
	if force {
		return nil
	}

	result, err := runGitCommand(ctx, repoRoot, nil, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(result.Stdout) != "" {
		return fmt.Errorf("%s requires a clean worktree and index; pass --force to skip this check", command)
	}

	return nil
}

func restoreCheckpoint(ctx context.Context, repoRoot string, meta checkpointRefInfo, force bool) error {
	if err := ensureCleanWorktree(ctx, repoRoot, "restore", force); err != nil {
		return err
	}

	parent, err := getCommitParent(ctx, repoRoot, meta.Commit)
	if err != nil {
		return err
	}

	return applyCheckpointDiff(ctx, repoRoot, "restored", parent, meta.Commit, conflictPolicyManual)
}

func pickCheckpoint(ctx context.Context, repoRoot, branchRef string, meta checkpointRefInfo, force bool, conflictPolicy string) error {
	if err := ensureCleanWorktree(ctx, repoRoot, "pick", force); err != nil {
		return err
	}

	patchRange, err := resolveCheckpointPatchRange(ctx, repoRoot, branchRef, meta.Ref)
	if err != nil {
		return err
	}

	return pickCheckpointPatchRange(ctx, repoRoot, patchRange, conflictPolicy)
}

func pickCheckpointRange(ctx context.Context, repoRoot, branchRef, arg string, force bool, conflictPolicy string) (checkpointPatchRange, error) {
	patchRange, err := resolveCheckpointPatchRange(ctx, repoRoot, branchRef, arg)
	if err != nil {
		return checkpointPatchRange{}, err
	}

	if err := ensureCleanWorktree(ctx, repoRoot, "pick", force); err != nil {
		return checkpointPatchRange{}, err
	}

	return patchRange, pickCheckpointPatchRange(ctx, repoRoot, patchRange, conflictPolicy)
}

func pickCheckpointPatchRange(ctx context.Context, repoRoot string, patchRange checkpointPatchRange, conflictPolicy string) error {
	return applyCheckpointDiff(ctx, repoRoot, "picked", patchRange.Base, patchRange.End.Commit, conflictPolicy)
}

func unpickCheckpointRange(ctx context.Context, repoRoot, branchRef, arg string, force bool, conflictPolicy string) (checkpointPatchRange, error) {
	patchRange, err := resolveCheckpointPatchRange(ctx, repoRoot, branchRef, arg)
	if err != nil {
		return checkpointPatchRange{}, err
	}

	if err := ensureCleanWorktree(ctx, repoRoot, "unpick", force); err != nil {
		return checkpointPatchRange{}, err
	}

	return patchRange, applyCheckpointDiff(ctx, repoRoot, "unpicked", patchRange.End.Commit, patchRange.Base, conflictPolicy)
}

func normalizeConflictPolicy(policy string) (string, error) {
	if policy == "" {
		return conflictPolicyManual, nil
	}
	switch policy {
	case conflictPolicyManual, conflictPolicyCheckpoint, conflictPolicyHead:
		return policy, nil
	default:
		return "", fmt.Errorf("invalid --conflict value %q (expected manual, checkpoint, head)", policy)
	}
}

func normalizeUnpickConflictPolicy(policy string) (string, error) {
	if policy == "" {
		return conflictPolicyManual, nil
	}
	switch policy {
	case conflictPolicyManual, conflictPolicyBase, conflictPolicyHead:
		return policy, nil
	default:
		return "", fmt.Errorf("invalid --conflict value %q (expected manual, base, head)", policy)
	}
}

func applyCheckpointDiff(ctx context.Context, repoRoot, action, base, target, conflictPolicy string) error {
	diff, err := runGitCommand(ctx, repoRoot, nil, "diff", "--binary", base, target)
	if err != nil {
		return err
	}
	if strings.TrimSpace(diff.Stdout) == "" {
		return nil
	}

	applyArgs := []string{"apply", "--binary", "--3way", "--index"}
	switch conflictPolicy {
	case conflictPolicyBase, conflictPolicyCheckpoint:
		applyArgs = append(applyArgs, "--theirs")
	case conflictPolicyHead:
		applyArgs = append(applyArgs, "--ours")
	}

	applyResult, err := runGitCommandWithInput(ctx, repoRoot, nil, diff.Stdout, applyArgs...)
	if err != nil {
		detail := strings.TrimSpace(applyResult.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(applyResult.Stdout)
		}
		if paths := parseApplyConflictPaths(detail); len(paths) > 0 || strings.Contains(strings.ToLower(detail), "conflict") {
			return patchConflictError{action: action, paths: paths, detail: detail}
		}
		return gitCommandError(err, applyResult)
	}

	return nil
}

func resolveCheckpointPatchRange(ctx context.Context, repoRoot, branchRef, arg string) (checkpointPatchRange, error) {
	startArg, endArg, ranged, err := splitCheckpointRangeArg(arg)
	if err != nil {
		return checkpointPatchRange{}, err
	}

	start, err := resolveShowCheckpointRefMetadata(ctx, repoRoot, branchRef, startArg)
	if err != nil {
		return checkpointPatchRange{}, err
	}

	end := start
	if ranged {
		end, err = resolveShowCheckpointRefMetadata(ctx, repoRoot, branchRef, endArg)
		if err != nil {
			return checkpointPatchRange{}, err
		}
	}

	startBranch := checkpointRefBranch(start, branchRef)
	endBranch := checkpointRefBranch(end, branchRef)
	if startBranch != endBranch {
		return checkpointPatchRange{}, fmt.Errorf("checkpoint range endpoints must be on the same autosnap branch: %s and %s", startBranch, endBranch)
	}

	checkpoints, err := listCheckpointRefsForBranch(ctx, repoRoot, startBranch)
	if err != nil {
		return checkpointPatchRange{}, err
	}

	startIndex := checkpointRefIndex(checkpoints, start.Ref)
	if startIndex < 0 {
		return checkpointPatchRange{}, fmt.Errorf("checkpoint not found in branch history: %s", start.Ref)
	}
	endIndex := checkpointRefIndex(checkpoints, end.Ref)
	if endIndex < 0 {
		return checkpointPatchRange{}, fmt.Errorf("checkpoint not found in branch history: %s", end.Ref)
	}
	if startIndex > endIndex {
		return checkpointPatchRange{}, fmt.Errorf("checkpoint range start must not be after range end")
	}

	base, err := resolveShowDiffBase(ctx, repoRoot, startBranch, start.Ref, start.Commit)
	if err != nil {
		return checkpointPatchRange{}, err
	}

	return checkpointPatchRange{
		Start: start,
		End:   end,
		Base:  base,
	}, nil
}

func splitCheckpointRangeArg(arg string) (string, string, bool, error) {
	if strings.Count(arg, "..") == 0 {
		return arg, arg, false, nil
	}
	if strings.Count(arg, "..") != 1 {
		return "", "", false, fmt.Errorf("invalid checkpoint range %q", arg)
	}

	parts := strings.SplitN(arg, "..", 2)
	start := strings.TrimSpace(parts[0])
	end := strings.TrimSpace(parts[1])
	if start == "" || end == "" {
		return "", "", false, fmt.Errorf("invalid checkpoint range %q", arg)
	}
	return start, end, true, nil
}

func checkpointRefBranch(meta checkpointRefInfo, fallback string) string {
	if meta.Branch != "" {
		return meta.Branch
	}
	if branch := branchFromCheckpointRef(meta.Ref); branch != "" {
		return branch
	}
	return fallback
}

func checkpointRefIndex(checkpoints []checkpointRefInfo, ref string) int {
	for i, checkpoint := range checkpoints {
		if checkpoint.Ref == ref {
			return i
		}
	}
	return -1
}

func parseApplyConflictPaths(output string) []string {
	seen := map[string]bool{}
	var paths []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "U ") {
			continue
		}
		path := strings.TrimSpace(strings.TrimPrefix(line, "U "))
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	return paths
}

func promoteCheckpoint(ctx context.Context, repoRoot string, meta checkpointRefInfo, force bool) (string, bool, error) {
	if err := ensureCleanWorktree(ctx, repoRoot, "promote", force); err != nil {
		return "", false, err
	}

	headResult, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", "HEAD")
	if err != nil {
		return "", false, err
	}
	head := strings.TrimSpace(headResult.Stdout)

	checkpointTree, err := getCheckpointTree(ctx, repoRoot, meta.Commit)
	if err != nil {
		return "", false, err
	}
	headTree, err := getCheckpointTree(ctx, repoRoot, "HEAD")
	if err != nil {
		return "", false, err
	}
	if checkpointTree == headTree {
		return head, false, nil
	}

	message, err := getCommitMessage(ctx, repoRoot, meta.Commit)
	if err != nil {
		return "", false, err
	}

	commitResult, err := runGitCommandWithInput(ctx, repoRoot, nil, strings.TrimSpace(message), "commit-tree", checkpointTree, "-p", head, "-F", "-")
	if err != nil {
		return "", false, err
	}
	commit := strings.TrimSpace(commitResult.Stdout)
	if commit == "" {
		return "", false, fmt.Errorf("promote did not create a commit")
	}

	if _, err := runGitCommand(ctx, repoRoot, nil, "reset", "--hard", commit); err != nil {
		return "", false, err
	}

	return commit, true, nil
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
		ts = checkpointRefTimestamp(ref)
	}

	return ref, ts, commit, nil
}

func listCheckpointRefsForBranch(ctx context.Context, repoRoot, branchRef string) ([]checkpointRefInfo, error) {
	refPrefix := snapshotRefPrefix(branchRef)
	return listCheckpointRefsUnderPrefix(ctx, repoRoot, refPrefix, branchRef)
}

func listCheckpointRefsForAllBranches(ctx context.Context, repoRoot string) ([]checkpointRefInfo, error) {
	return listCheckpointRefsUnderPrefix(ctx, repoRoot, path.Join("refs", "autosnapshots"), "")
}

func listCheckpointRefsUnderPrefix(ctx context.Context, repoRoot, refPrefix, branchRef string) ([]checkpointRefInfo, error) {
	refPattern := refPrefix + "/*"
	if branchRef == "" {
		refPattern = refPrefix
	}

	result, err := runGitCommand(
		ctx,
		repoRoot,
		nil,
		"for-each-ref",
		"--sort=refname",
		"--format=%(refname) %(objectname:short) %(objectname)",
		refPattern,
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
		fullSHA := sha
		if len(parts) >= 3 {
			fullSHA = parts[2]
		}
		timestamp := checkpointRefTimestamp(refName)
		branch := branchRef
		if branch == "" {
			branch = branchFromCheckpointRef(refName)
		}
		refs = append(refs, checkpointRefInfo{
			Ref:        refName,
			Commit:     sha,
			FullCommit: fullSHA,
			Timestamp:  timestamp,
			Branch:     branch,
		})
	}

	return refs, nil
}

func branchFromCheckpointRef(ref string) string {
	const prefix = "refs/autosnapshots/"
	if !strings.HasPrefix(ref, prefix) {
		return ""
	}

	withoutPrefix := strings.TrimPrefix(ref, prefix)
	timestamp := path.Base(ref)
	return strings.TrimSuffix(withoutPrefix, "/"+timestamp)
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

	entries, err := listCheckpointRefsForBranch(ctx, repoRoot, branchRef)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("no checkpoints for current branch")
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
			Ref:        ref,
			Commit:     shortObjectID(commit),
			FullCommit: commit,
			Timestamp:  checkpointRefTimestamp(ref),
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
		Ref:        ref,
		Commit:     shortObjectID(commit),
		FullCommit: commit,
		Timestamp:  checkpointRefTimestamp(ref),
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

func filterCheckpointRefsByCommit(entries []checkpointRefInfo, commit string, prefix bool) []checkpointRefInfo {
	normalized := strings.ToLower(commit)
	var matches []checkpointRefInfo
	for _, entry := range entries {
		cand := strings.ToLower(checkpointRefCommit(entry))
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

func checkpointRefCommit(entry checkpointRefInfo) string {
	if strings.TrimSpace(entry.FullCommit) != "" {
		return strings.TrimSpace(entry.FullCommit)
	}
	return strings.TrimSpace(entry.Commit)
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

func checkpointListSummary(message string) string {
	line := strings.TrimSpace(strings.SplitN(message, "\n", 2)[0])
	if line == "" {
		return "unknown"
	}

	status, checkCmd := parseCheckpointMessage(message)
	if status == "unknown" && checkCmd == "" {
		return line
	}
	if checkCmd == "" {
		return status
	}
	return strings.TrimSpace(status + " " + checkCmd)
}

var checkpointStatusRegex = regexp.MustCompile(`^autosnap:\s+(passing|failing)\s+checkpoint\b`)

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
	return listCheckpointsFromRefs(ctx, repoRoot, entries)
}

const failedCommitMetadataSummary = "failed to read commit metadata"

type checkpointSubjectMetadata struct {
	subject string
	found   bool
}

func listCheckpointsFromRefs(ctx context.Context, repoRoot string, entries []checkpointRefInfo) ([]checkpointInfo, error) {
	if len(entries) == 0 {
		return nil, nil
	}

	subjectByRef := batchCheckpointSubjects(ctx, repoRoot, entries)

	checkpoints := make([]checkpointInfo, 0, len(entries))
	for _, entry := range entries {
		checkCmd := ""
		status := "unknown"
		summary := "unknown"

		meta := subjectByRef[entry.Ref]
		if !meta.found {
			summary = failedCommitMetadataSummary
		} else {
			status, checkCmd = parseCheckpointMessage(meta.subject)
			summary = checkpointListSummary(meta.subject)
		}

		checkpoints = append(checkpoints, checkpointInfo{
			Ref:       entry.Ref,
			Commit:    entry.Commit,
			Timestamp: entry.Timestamp,
			Branch:    entry.Branch,
			Status:    status,
			CheckCmd:  checkCmd,
			Summary:   summary,
		})
	}

	sort.Slice(checkpoints, func(i, j int) bool {
		if checkpoints[i].Timestamp != checkpoints[j].Timestamp {
			return checkpoints[i].Timestamp < checkpoints[j].Timestamp
		}
		if checkpoints[i].Branch != checkpoints[j].Branch {
			return checkpoints[i].Branch < checkpoints[j].Branch
		}
		return checkpoints[i].Ref < checkpoints[j].Ref
	})

	return checkpoints, nil
}

func batchCheckpointSubjects(ctx context.Context, repoRoot string, entries []checkpointRefInfo) map[string]checkpointSubjectMetadata {
	subjectByRef := map[string]checkpointSubjectMetadata{}
	if len(entries) == 0 {
		return subjectByRef
	}

	args := []string{
		"for-each-ref",
		"--sort=refname",
		"--format=%(refname)\x1f%(contents:lines=1)",
	}
	for _, entry := range entries {
		args = append(args, entry.Ref)
	}

	result, err := runGitCommand(ctx, repoRoot, nil, args...)
	if err != nil {
		return subjectByRef
	}

	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, "\x1f", 2)
		if len(parts) != 2 {
			continue
		}
		ref := strings.TrimSpace(parts[0])
		if ref == "" {
			continue
		}
		subjectByRef[ref] = checkpointSubjectMetadata{
			subject: strings.TrimSpace(parts[1]),
			found:   true,
		}
	}

	return subjectByRef
}
