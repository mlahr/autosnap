package autosnap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

var ignoredPathSegments = map[string]struct{}{
	".git":         {},
	".idea":        {},
	".vscode":      {},
	".DS_Store":    {},
	"node_modules": {},
	"target":       {},
	"build":        {},
	"dist":         {},
	"out":          {},
	".gradle":      {},
}

const (
	watchModeRecursive  = "recursive"
	watchModePoll       = "poll"
	watchModeAuto       = "auto"
	defaultPollInterval = 5 * time.Second
)

type snapshotRunner struct {
	ctx          context.Context
	repoRoot     string
	branchRef    string
	checkCmd     string
	msgSourceCmd string
	snapshotMode string
	commitMode   string
	watchMode    string
	pollInterval time.Duration
	idle         time.Duration

	statePath string
	state     autosnapState

	watcher *fsnotify.Watcher

	mu               sync.Mutex
	timer            *time.Timer
	checking         bool
	pendingAfterRun  bool
	lastChange       time.Time
	stopped          bool
	ignoreCache      map[string]bool
	ignoreCacheMu    sync.RWMutex
	watchIgnoreRules []ignoreRule
}

func newSnapshotRunner(ctx context.Context, repoRoot, branchRef, checkCommand, msgSourceCommand, snapshotMode string, idle time.Duration, statePath string) (*snapshotRunner, error) {
	return newSnapshotRunnerWithWatch(ctx, repoRoot, branchRef, checkCommand, msgSourceCommand, snapshotMode, commitModeCheckpoint, watchModeRecursive, defaultPollInterval, idle, statePath)
}

func newSnapshotRunnerWithWatch(ctx context.Context, repoRoot, branchRef, checkCommand, msgSourceCommand, snapshotMode, commitMode, watchMode string, pollInterval, idle time.Duration, statePath string) (*snapshotRunner, error) {
	state, err := loadAutosnapState(statePath)
	if err != nil {
		return nil, err
	}
	if state.RepoRoot == "" {
		state.RepoRoot = repoRoot
	}
	normalizedWatchMode, err := normalizeWatchMode(watchMode)
	if err != nil {
		return nil, err
	}
	normalizedCommitMode, err := normalizeCommitMode(commitMode)
	if err != nil {
		return nil, err
	}
	if normalizedCommitMode == commitModeDirect && snapshotMode != snapshotModeBoth {
		return nil, errors.New("commit_mode direct requires snapshot_mode both")
	}
	if pollInterval <= 0 {
		return nil, errors.New("poll interval must be greater than 0")
	}
	watchIgnoreRules, err := loadAutosnapIgnoreRules(repoRoot)
	if err != nil {
		return nil, err
	}

	return &snapshotRunner{
		ctx:          ctx,
		repoRoot:     repoRoot,
		branchRef:    branchRef,
		checkCmd:     checkCommand,
		msgSourceCmd: msgSourceCommand,
		snapshotMode: snapshotMode,
		commitMode:   normalizedCommitMode,
		watchMode:    normalizedWatchMode,
		pollInterval: pollInterval,
		idle:         idle,
		statePath:    statePath,
		state:        state,
		ignoreCache: map[string]bool{
			"": false,
		},
		watchIgnoreRules: watchIgnoreRules,
	}, nil
}

func (r *snapshotRunner) start() error {
	switch r.watchMode {
	case watchModePoll:
		return r.startPolling()
	case watchModeAuto:
		err := r.startRecursive()
		if isWatchLimitError(err) {
			logf("recursive watcher hit file limit; falling back to polling every %s\n", r.pollInterval)
			r.closeWatcher()
			return r.startPolling()
		}
		return err
	default:
		return r.startRecursive()
	}
}

func (r *snapshotRunner) startRecursive() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	r.watcher = watcher
	defer watcher.Close()

	if err := r.watchDirectoryTree(r.repoRoot); err != nil {
		return err
	}

	r.mu.Lock()
	r.lastChange = time.Now()
	r.scheduleTimerLocked(r.idle)
	r.mu.Unlock()

	for {
		select {
		case <-r.ctx.Done():
			r.stop()
			return nil
		case event := <-watcher.Events:
			if err := r.handleEvent(event); err != nil {
				if errors.Is(err, fsnotify.ErrEventOverflow) {
					logf("watch overflow: %v\n", err)
					continue
				}
				return err
			}
		case err := <-watcher.Errors:
			if err != nil {
				logf("watch error: %v\n", err)
			}
		}
	}
}

func (r *snapshotRunner) startPolling() error {
	r.mu.Lock()
	r.lastChange = time.Now()
	r.scheduleTimerLocked(r.idle)
	r.mu.Unlock()

	lastSignature, err := r.pollChangeSignature()
	if err != nil {
		logf("unable to read initial poll state: %v\n", err)
	}

	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			r.stop()
			return nil
		case <-ticker.C:
			signature, err := r.pollChangeSignature()
			if err != nil {
				logf("poll error: %v\n", err)
				continue
			}
			if signature != lastSignature {
				lastSignature = signature
				if signature != "" {
					logln("changed: polled working tree")
					r.touch()
				}
			}
		}
	}
}

func (r *snapshotRunner) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.stopped = true
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
	if r.watcher != nil {
		r.closeWatcherLocked()
	}
}

func (r *snapshotRunner) closeWatcher() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeWatcherLocked()
}

func (r *snapshotRunner) closeWatcherLocked() {
	if r.watcher == nil {
		return
	}
	_ = r.watcher.Close()
	r.watcher = nil
}

func (r *snapshotRunner) scheduleTimerLocked(d time.Duration) {
	if r.stopped {
		return
	}
	if r.timer != nil {
		r.timer.Stop()
	}
	r.timer = time.AfterFunc(d, r.runIdleCheck)
}

func (r *snapshotRunner) touch() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastChange = time.Now()
	if r.checking {
		r.pendingAfterRun = true
		return
	}
	r.scheduleTimerLocked(r.idle)
}

func (r *snapshotRunner) runIdleCheck() {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	if r.checking {
		r.pendingAfterRun = true
		r.mu.Unlock()
		return
	}

	r.checking = true
	r.mu.Unlock()

	logln("idle reached, running check...")
	r.runCheck()

	r.mu.Lock()
	r.checking = false
	if r.pendingAfterRun {
		delay := r.idle
		if elapsed := time.Since(r.lastChange); elapsed < delay {
			delay = delay - elapsed
		} else {
			delay = 0
		}
		r.pendingAfterRun = false
		r.scheduleTimerLocked(delay)
	}
	r.mu.Unlock()
}

func (r *snapshotRunner) runCheck() {
	position, err := currentGitPosition(r.ctx, r.repoRoot)
	if err != nil {
		logf("unable to resolve current HEAD: %v\n", err)
		return
	}
	branchRef := position.BranchRef
	previousState := r.state

	duration, exitCode, err := runShellCheck(r.ctx, r.repoRoot, r.checkCmd)
	r.state.LastBranch = branchRef
	r.state.RepoRoot = r.repoRoot
	r.state.LastCheckAt = time.Now().UTC().Format(time.RFC3339)
	r.state.LastCheckStatus = "passed"
	r.state.LastCheckCommand = r.checkCmd
	r.state.LastCheckDurationMs = int64(duration / time.Millisecond)

	if exitCode != 0 {
		r.state.LastCheckStatus = "failed"
		r.state.LastFailureAt = r.state.LastCheckAt
		r.state.LastFailureExitCode = exitCode
		_ = saveAutosnapState(r.statePath, r.state)
		logln("check failed; checkpoint not created")
		return
	}

	_ = saveAutosnapState(r.statePath, r.state)

	gitDirectory, err := gitDir(r.ctx, r.repoRoot)
	if err != nil {
		logf("unable to resolve git directory: %v\n", err)
		return
	}
	tree, err := computeWorktreeTree(r.ctx, r.repoRoot, gitDirectory, r.snapshotMode)
	if err != nil {
		logf("unable to compute working tree tree: %v\n", err)
		return
	}

	previousCheckpointRef, previousCheckpointTree := resolvePreviousCheckpoint(r.ctx, r.repoRoot, branchRef, previousState)

	if r.commitMode == commitModeCheckpoint && previousCheckpointTree != "" && previousCheckpointTree == tree {
		logln("no meaningful diff; checkpoint skipped")
		return
	}

	commitMessage := ""
	if strings.TrimSpace(r.msgSourceCmd) != "" {
		diffBase := previousCheckpointRef
		if diffBase == "" {
			diffBase = position.Head
		}
		env := map[string]string{
			"AUTOSNAP_DIFF_BASE":               diffBase,
			"AUTOSNAP_PREVIOUS_CHECKPOINT_REF": previousCheckpointRef,
			"AUTOSNAP_BRANCH_REF":              branchRef,
			"AUTOSNAP_HEAD":                    position.Head,
		}
		message, sourceExitCode, err := runShellOutputEnv(r.ctx, r.repoRoot, r.msgSourceCmd, env)
		if err != nil || sourceExitCode != 0 {
			logln("msg-source-cmd failed; using generated checkpoint message")
		} else {
			commitMessage = strings.TrimSpace(message)
			if commitMessage == "" {
				logln("msg-source-cmd produced no output; using generated checkpoint message")
			}
		}
	}

	switch r.commitMode {
	case commitModeDirect:
		commit, created, ts, err := createDirectCommitChecked(r.ctx, r.repoRoot, branchRef, position.Head, r.checkCmd, r.idle, tree, commitMessage)
		if err != nil {
			logf("unable to create direct commit: %v\n", err)
			return
		}
		if !created {
			logln("no meaningful diff; direct commit skipped")
			return
		}

		r.state.LastCheckpointRef = commit
		r.state.LastCheckpointAt = ts
		r.state.LastCheckpointTree = tree
		if err := saveAutosnapState(r.statePath, r.state); err != nil {
			logf("unable to persist state: %v\n", err)
		}

		commitShort := commit
		if len(commitShort) > 7 {
			commitShort = commitShort[:7]
		}
		logf("direct commit saved: %s\n", commitShort)
	default:
		ref, commit, err := createCheckpointChecked(r.ctx, r.repoRoot, branchRef, position.Head, r.checkCmd, r.idle, tree, commitMessage)
		if err != nil {
			logf("unable to create checkpoint: %v\n", err)
			return
		}

		r.state.LastCheckpointRef = ref
		r.state.LastCheckpointAt = pathBase(ref)
		r.state.LastCheckpointTree = tree
		if err := saveAutosnapState(r.statePath, r.state); err != nil {
			logf("unable to persist state: %v\n", err)
		}

		commitShort := commit
		if len(commitShort) > 7 {
			commitShort = commitShort[:7]
		}
		logf("checkpoint saved: %s\n", commitShort)
	}
}

func resolvePreviousCheckpoint(ctx context.Context, repoRoot, branchRef string, state autosnapState) (string, string) {
	if state.LastBranch == branchRef && state.LastCheckpointRef != "" {
		tree := state.LastCheckpointTree
		if tree == "" {
			if resolvedTree, err := getCheckpointTree(ctx, repoRoot, state.LastCheckpointRef); err == nil {
				tree = resolvedTree
			}
		}
		return state.LastCheckpointRef, tree
	}

	ref, _, _, err := getLatestCheckpointForBranch(ctx, repoRoot, branchRef)
	if err != nil || ref == "" {
		return "", ""
	}

	tree, _ := getCheckpointTree(ctx, repoRoot, ref)
	return ref, tree
}

func (r *snapshotRunner) handleEvent(event fsnotify.Event) error {
	rel, err := filepath.Rel(r.repoRoot, event.Name)
	if err != nil {
		return nil
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return nil
	}
	if r.shouldIgnorePath(rel) {
		return nil
	}

	if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) || event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) || event.Has(fsnotify.Chmod) {
		logf("changed: %s\n", rel)
		r.touch()
	}

	if event.Has(fsnotify.Create) {
		info, err := os.Stat(event.Name)
		if err == nil && info.IsDir() {
			return r.watchDirectoryTree(event.Name)
		}
	}

	return nil
}

func (r *snapshotRunner) currentBranchRef() (string, error) {
	position, err := currentGitPosition(r.ctx, r.repoRoot)
	if err != nil {
		return "", err
	}
	return position.BranchRef, nil
}

func (r *snapshotRunner) watchDirectoryTree(root string) error {
	return filepath.WalkDir(root, func(name string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(r.repoRoot, name)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if r.shouldIgnorePath(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		return r.watcher.Add(name)
	})
}

func (r *snapshotRunner) pollChangeSignature() (string, error) {
	result, err := runGitCommand(r.ctx, r.repoRoot, nil, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return "", err
	}

	mode, err := normalizeSnapshotMode(r.snapshotMode)
	if err != nil {
		return "", err
	}

	entries := parsePorcelainStatusEntries(result.Stdout)
	parts := make([]string, 0, len(entries)*2)
	for _, entry := range entries {
		entry.path = filepath.ToSlash(entry.path)
		if entry.previousPath != "" {
			entry.previousPath = filepath.ToSlash(entry.previousPath)
		}
		currentIgnored := entry.path == "" || r.shouldIgnorePath(entry.path)

		if !currentIgnored {
			if mode == snapshotModeBoth || mode == snapshotModeStaged {
				if entry.hasIndexChange() {
					state, err := r.indexPathSignature(entry.path, entry.indexStatus())
					if err != nil {
						return "", err
					}
					parts = append(parts, "index\x00"+entry.path+"\x00"+state)
				}
			}

			if mode == snapshotModeBoth || mode == snapshotModeWorking {
				if entry.hasWorkingChange() {
					state, err := r.workingPathSignature(entry.path, entry.worktreeStatus())
					if err != nil {
						return "", err
					}
					parts = append(parts, "working\x00"+entry.path+"\x00"+state)
				}
			}
		}

		if entry.previousPath != "" && !r.shouldIgnorePath(entry.previousPath) {
			parts = append(parts, "previous\x00"+entry.previousPath+"\x00"+entry.status)
		}
	}
	sort.Strings(parts)
	return strings.Join(parts, "\x00"), nil
}

func (r *snapshotRunner) workingPathSignature(relPath, status string) (string, error) {
	path := filepath.Join(r.repoRoot, filepath.FromSlash(relPath))
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return status + "\x00missing", nil
		}
		return "", err
	}

	mode := info.Mode()
	if mode&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256([]byte(target))
		return fmt.Sprintf("%s\x00symlink\x00%s\x00%s", status, mode.String(), hex.EncodeToString(sum[:])), nil
	}

	if !mode.IsRegular() {
		return fmt.Sprintf("%s\x00other\x00%s", status, mode.String()), nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(content)
	return fmt.Sprintf("%s\x00file\x00%s\x00%d\x00%s", status, mode.String(), info.Size(), hex.EncodeToString(sum[:])), nil
}

func (r *snapshotRunner) indexPathSignature(relPath, status string) (string, error) {
	result, err := runGitCommand(r.ctx, r.repoRoot, nil, "ls-files", "-s", "-z", "--", relPath)
	if err != nil {
		return "", err
	}
	raw := strings.TrimRight(result.Stdout, "\x00")
	if raw == "" {
		return status + "\x00missing", nil
	}
	return status + "\x00" + raw, nil
}

func (r *snapshotRunner) shouldIgnorePath(relPath string) bool {
	if relPath == "." {
		return false
	}

	if ignored, ok := r.getIgnoredFromCache(relPath); ok {
		return ignored
	}

	segments := strings.Split(relPath, "/")
	for _, segment := range segments {
		if _, ok := ignoredPathSegments[segment]; ok {
			r.setIgnoredInCache(relPath, true)
			return true
		}
	}

	if matchAutosnapIgnoreRules(r.watchIgnoreRules, relPath) {
		r.setIgnoredInCache(relPath, true)
		return true
	}

	ignoredByGit, err := isGitIgnored(context.Background(), r.repoRoot, relPath)
	if err != nil {
		return false
	}

	r.setIgnoredInCache(relPath, ignoredByGit)
	return ignoredByGit
}

func (r *snapshotRunner) getIgnoredFromCache(relPath string) (bool, bool) {
	r.ignoreCacheMu.RLock()
	defer r.ignoreCacheMu.RUnlock()
	ignored, ok := r.ignoreCache[relPath]
	return ignored, ok
}

func (r *snapshotRunner) setIgnoredInCache(relPath string, ignored bool) {
	r.ignoreCacheMu.Lock()
	defer r.ignoreCacheMu.Unlock()
	r.ignoreCache[relPath] = ignored
}

func pathBase(raw string) string {
	if idx := strings.LastIndex(raw, "/"); idx != -1 {
		raw = raw[idx+1:]
	}
	return raw
}

func normalizeWatchMode(mode string) (string, error) {
	if mode == "" {
		return watchModeRecursive, nil
	}
	switch mode {
	case watchModeRecursive, watchModePoll, watchModeAuto:
		return mode, nil
	default:
		return "", errors.New("invalid watch mode")
	}
}

func isWatchLimitError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EMFILE) || errors.Is(err, syscall.ENFILE) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "too many open files")
}

func parsePorcelainStatusPaths(raw string) []string {
	entries := parsePorcelainStatusEntries(raw)
	paths := make([]string, 0, len(entries)*2)
	for _, entry := range entries {
		if entry.previousPath != "" {
			paths = append(paths, entry.previousPath)
		}
		paths = append(paths, entry.path)
	}
	return paths
}

type porcelainStatusEntry struct {
	status       string
	path         string
	previousPath string
}

func (e porcelainStatusEntry) indexStatus() string {
	if len(e.status) < 1 {
		return ""
	}
	return e.status[:1]
}

func (e porcelainStatusEntry) worktreeStatus() string {
	if len(e.status) < 2 {
		return ""
	}
	return e.status[1:2]
}

func (e porcelainStatusEntry) hasIndexChange() bool {
	if e.status == "??" {
		return false
	}
	return e.indexStatus() != " "
}

func (e porcelainStatusEntry) hasWorkingChange() bool {
	if e.status == "??" {
		return true
	}
	return e.worktreeStatus() != " "
}

func parsePorcelainStatusEntries(raw string) []porcelainStatusEntry {
	if raw == "" {
		return nil
	}
	fields := strings.Split(raw, "\x00")
	entries := make([]porcelainStatusEntry, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if entry == "" || len(entry) < 4 {
			continue
		}
		status := entry[:2]
		pathName := entry[3:]
		statusEntry := porcelainStatusEntry{
			status: status,
			path:   pathName,
		}
		if status[0] == 'R' || status[0] == 'C' {
			if i+1 < len(fields) && fields[i+1] != "" {
				statusEntry.previousPath = fields[i+1]
				i++
			}
		}
		entries = append(entries, statusEntry)
	}
	return entries
}
