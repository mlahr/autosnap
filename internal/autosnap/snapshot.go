package autosnap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

type snapshotRunner struct {
	ctx       context.Context
	repoRoot  string
	branchRef string
	checkCmd  string
	idle      time.Duration

	statePath string
	state     autosnapState

	watcher *fsnotify.Watcher

	mu              sync.Mutex
	timer           *time.Timer
	checking        bool
	pendingAfterRun bool
	lastChange      time.Time
	stopped         bool
	ignoreCache     map[string]bool
	ignoreCacheMu   sync.RWMutex
}

func newSnapshotRunner(ctx context.Context, repoRoot, branchRef, checkCommand string, idle time.Duration, statePath string) (*snapshotRunner, error) {
	state, err := loadAutosnapState(statePath)
	if err != nil {
		return nil, err
	}
	if state.RepoRoot == "" {
		state.RepoRoot = repoRoot
	}

	return &snapshotRunner{
		ctx:       ctx,
		repoRoot:  repoRoot,
		branchRef: branchRef,
		checkCmd:  checkCommand,
		idle:      idle,
		statePath: statePath,
		state:     state,
		ignoreCache: map[string]bool{
			"": false,
		},
	}, nil
}

func (r *snapshotRunner) start() error {
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
					fmt.Println("watch overflow:", err)
					continue
				}
				return err
			}
		case err := <-watcher.Errors:
			if err != nil {
				fmt.Println("watch error:", err)
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

	fmt.Println("idle reached, running check...")
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
	branchRef, err := r.currentBranchRef()
	if err != nil {
		fmt.Println("unable to resolve current branch:", err)
		return
	}

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
		fmt.Println("check failed; checkpoint not created")
		return
	}

	_ = saveAutosnapState(r.statePath, r.state)

	gitDirectory, err := gitDir(r.ctx, r.repoRoot)
	if err != nil {
		fmt.Println("unable to resolve git directory:", err)
		return
	}
	tree, err := computeWorktreeTree(r.ctx, r.repoRoot, gitDirectory)
	if err != nil {
		fmt.Println("unable to compute working tree tree:", err)
		return
	}

	lastTree := ""
	if r.state.LastBranch == r.branchRef && r.state.LastCheckpointTree != "" {
		lastTree = r.state.LastCheckpointTree
	}
	if lastTree == "" {
		ref, _, _, err := getLatestCheckpointForBranch(r.ctx, r.repoRoot, branchRef)
		if err == nil && ref != "" {
			lastTree, _ = getCheckpointTree(r.ctx, r.repoRoot, ref)
		}
	}

	if lastTree != "" && lastTree == tree {
		fmt.Println("no meaningful diff; checkpoint skipped")
		return
	}

	ref, commit, err := createCheckpoint(r.ctx, r.repoRoot, branchRef, r.checkCmd, r.idle, tree)
	if err != nil {
		fmt.Println("unable to create checkpoint:", err)
		return
	}

	r.state.LastCheckpointRef = ref
	r.state.LastCheckpointAt = pathBase(ref)
	r.state.LastCheckpointTree = tree
	if err := saveAutosnapState(r.statePath, r.state); err != nil {
		fmt.Println("unable to persist state:", err)
	}

	commitShort := commit
	if len(commitShort) > 7 {
		commitShort = commitShort[:7]
	}
	fmt.Printf("checkpoint saved: %s\n", commitShort)
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
		fmt.Printf("changed: %s\n", rel)
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
	result, err := runGitCommand(r.ctx, r.repoRoot, nil, "branch", "--show-current")
	if err != nil {
		return "", err
	}

	branchRef := strings.TrimSpace(result.Stdout)
	if branchRef != "" {
		return branchRef, nil
	}

	head, err := runGitCommand(r.ctx, r.repoRoot, nil, "rev-parse", "--short", "HEAD")
	if err != nil {
		return "", err
	}

	headSHA := strings.TrimSpace(head.Stdout)
	if headSHA == "" {
		return "detached", nil
	}

	return "detached-" + headSHA, nil
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
