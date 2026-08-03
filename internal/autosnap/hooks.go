package autosnap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	managedHookMarker = "# autosnap-managed-hook:v1"
	hookBackupSuffix  = ".autosnap-backup"
)

var autosnapExecutable = os.Executable
var ensureAutosnapRunningForHooks = ensureAutosnapRunning

type hookTarget struct {
	name       string
	path       string
	backupPath string
}

type hookSnapshot struct {
	path      string
	exists    bool
	mode      os.FileMode
	content   []byte
	symlink   string
	isSymlink bool
}

func newHooksCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "hooks", Short: "Manage autosnap Git hooks"}
	cmd.AddCommand(newHooksInstallCommand())
	cmd.AddCommand(newHooksStatusCommand())
	cmd.AddCommand(newHooksUninstallCommand())
	return cmd
}

func newHooksInstallCommand() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install hooks that keep autosnap running",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return installAutosnapHooks(context.Background(), cmd.OutOrStdout(), cmd.ErrOrStderr(), force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Acknowledge a custom hooks path or back up and chain existing hooks")
	return cmd
}

func newHooksStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show autosnap Git hook status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return showAutosnapHooksStatus(context.Background(), cmd.OutOrStdout())
		},
	}
}

func newHooksUninstallCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Remove autosnap Git hooks and restore chained hooks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return uninstallAutosnapHooks(context.Background(), cmd.OutOrStdout())
		},
	}
}

func installAutosnapHooks(ctx context.Context, out, stderr io.Writer, force bool) error {
	repoRoot, _, _, err := detectRepository(ctx)
	if err != nil {
		return err
	}
	if err := validateHookConfig(repoRoot); err != nil {
		return err
	}
	tracked, err := autosnapConfigTracked(ctx, repoRoot)
	if err != nil {
		return err
	}
	if !tracked {
		fmt.Fprintf(stderr, "warning: %s is not tracked; new worktrees may not contain autosnap configuration\n", autosnapConfigFileName)
	}

	hooksDir, custom, customValue, err := resolveHooksDirectory(ctx, repoRoot)
	if err != nil {
		return err
	}
	if custom && !force {
		return fmt.Errorf("core.hooksPath is set to %q; rerun with --force to install into %s", customValue, hooksDir)
	}
	if custom {
		fmt.Fprintf(stderr, "warning: installing into custom core.hooksPath %q resolved as %s; linked-worktree coverage depends on that path's scope\n", customValue, hooksDir)
	}

	executable, err := autosnapExecutable()
	if err != nil {
		return err
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return err
	}
	targets := autosnapHookTargets(hooksDir)
	for _, target := range targets {
		if err := preflightHookInstall(target, force); err != nil {
			return err
		}
	}

	snapshots, err := snapshotHookPaths(targets)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return err
	}
	for _, target := range targets {
		if err := installHookTarget(target, executable, force); err != nil {
			_ = restoreHookSnapshots(snapshots)
			return err
		}
	}

	fmt.Fprintf(out, "autosnap hooks installed: %s\n", hooksDir)
	if err := ensureAutosnapRunningForHooks(ctx, repoRoot, out); err != nil {
		return fmt.Errorf("hooks installed, but autosnap could not be started: %w", err)
	}
	return nil
}

func validateHookConfig(repoRoot string) error {
	if _, err := os.Stat(autosnapConfigPath(repoRoot)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s does not exist; run 'autosnap config init' before installing hooks", autosnapConfigPath(repoRoot))
		}
		return err
	}
	_, found, err := resolveAutosnapConfig(repoRoot, autosnapConfigOverrides{set: map[string]bool{}}, true)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("%s does not exist; run 'autosnap config init' before installing hooks", autosnapConfigPath(repoRoot))
	}
	return nil
}

func autosnapConfigTracked(ctx context.Context, repoRoot string) (bool, error) {
	result, err := runGitCommand(ctx, repoRoot, nil, "ls-files", "--error-unmatch", "--", autosnapConfigFileName)
	if err != nil {
		if result.ExitCode == 1 {
			return false, nil
		}
		return false, gitCommandError(err, result)
	}
	return true, nil
}

func resolveHooksDirectory(ctx context.Context, repoRoot string) (string, bool, string, error) {
	configured, configErr := runGitCommand(ctx, repoRoot, nil, "config", "--get", "core.hooksPath")
	custom := configErr == nil && strings.TrimSpace(configured.Stdout) != ""
	if configErr != nil && configured.ExitCode != 1 {
		return "", false, "", gitCommandError(configErr, configured)
	}
	result, err := runGitCommand(ctx, repoRoot, nil, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", false, "", gitCommandError(err, result)
	}
	path := filepath.Clean(strings.TrimSpace(result.Stdout))
	if !filepath.IsAbs(path) {
		path = filepath.Join(repoRoot, path)
	}
	return filepath.Clean(path), custom, strings.TrimSpace(configured.Stdout), nil
}

func autosnapHookTargets(hooksDir string) []hookTarget {
	targets := make([]hookTarget, 0, 2)
	for _, name := range []string{"post-checkout", "pre-commit"} {
		path := filepath.Join(hooksDir, name)
		targets = append(targets, hookTarget{name: name, path: path, backupPath: path + hookBackupSuffix})
	}
	return targets
}

func preflightHookInstall(target hookTarget, force bool) error {
	exists, err := hookPathExists(target.path)
	if err != nil {
		return err
	}
	if exists {
		raw, readErr := os.ReadFile(target.path)
		if readErr == nil && validManagedHook(raw) {
			return nil
		}
		if readErr == nil && bytes.Contains(raw, []byte(managedHookMarker)) {
			return fmt.Errorf("%s is a modified autosnap-managed hook; refusing to overwrite it", target.path)
		}
		if !force {
			return fmt.Errorf("%s already exists; rerun with --force to back it up and chain it", target.path)
		}
		if _, backupErr := os.Lstat(target.backupPath); backupErr == nil {
			return fmt.Errorf("backup path already exists: %s", target.backupPath)
		} else if !os.IsNotExist(backupErr) {
			return backupErr
		}
		return nil
	}
	if _, backupErr := os.Lstat(target.backupPath); backupErr == nil {
		return fmt.Errorf("backup path exists without a managed hook: %s", target.backupPath)
	} else if !os.IsNotExist(backupErr) {
		return backupErr
	}
	return nil
}

func hookPathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func installHookTarget(target hookTarget, executable string, force bool) error {
	exists, err := hookPathExists(target.path)
	if err != nil {
		return err
	}
	managed := false
	if exists {
		raw, readErr := os.ReadFile(target.path)
		managed = readErr == nil && validManagedHook(raw)
	}
	if exists && !managed {
		if !force {
			return fmt.Errorf("%s already exists", target.path)
		}
		if err := os.Rename(target.path, target.backupPath); err != nil {
			return err
		}
	}
	_, backupErr := os.Lstat(target.backupPath)
	backupExists := backupErr == nil
	if backupErr != nil && !os.IsNotExist(backupErr) {
		return backupErr
	}
	return writeFileAtomic(target.path, renderManagedHook(executable, target.backupPath, backupExists), 0o755)
}

func renderManagedHook(executable, backupPath string, backupExists bool) []byte {
	body := &strings.Builder{}
	fmt.Fprintf(body, "autosnap_executable=%s\n", shellQuote(filepath.ToSlash(executable)))
	body.WriteString("if ! \"$autosnap_executable\" ensure-running; then\n")
	body.WriteString("\tprintf '%s\\n' 'warning: autosnap could not be started; continuing Git operation' >&2\n")
	body.WriteString("fi\n")
	if backupExists {
		fmt.Fprintf(body, "autosnap_backup=%s\n", shellQuote(filepath.ToSlash(backupPath)))
		body.WriteString("if [ -f \"$autosnap_backup\" ] && [ -x \"$autosnap_backup\" ]; then\n")
		body.WriteString("\t\"$autosnap_backup\" \"$@\"\n")
		body.WriteString("\texit $?\n")
		body.WriteString("fi\n")
	}
	body.WriteString("exit 0\n")
	bodyRaw := []byte(body.String())
	hash := sha256.Sum256(bodyRaw)
	return []byte(fmt.Sprintf("#!/bin/sh\n%s\n# autosnap-body-sha256:%x\n%s", managedHookMarker, hash, bodyRaw))
}

func validManagedHook(raw []byte) bool {
	lines := bytes.SplitN(raw, []byte("\n"), 4)
	if len(lines) != 4 || string(lines[1]) != managedHookMarker {
		return false
	}
	const prefix = "# autosnap-body-sha256:"
	if !strings.HasPrefix(string(lines[2]), prefix) {
		return false
	}
	hash := sha256.Sum256(lines[3])
	return string(lines[2]) == fmt.Sprintf("%s%x", prefix, hash)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func showAutosnapHooksStatus(ctx context.Context, out io.Writer) error {
	repoRoot, _, _, err := detectRepository(ctx)
	if err != nil {
		return err
	}
	hooksDir, custom, customValue, err := resolveHooksDirectory(ctx, repoRoot)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "hooks path: %s\n", hooksDir)
	if custom {
		fmt.Fprintf(out, "core.hooksPath: %s\n", customValue)
	} else {
		fmt.Fprintln(out, "core.hooksPath: default")
	}
	for _, target := range autosnapHookTargets(hooksDir) {
		fmt.Fprintf(out, "%s: %s\n", target.name, hookTargetStatus(target))
	}
	return nil
}

func hookTargetStatus(target hookTarget) string {
	exists, err := hookPathExists(target.path)
	if err != nil {
		return "conflicting"
	}
	if !exists {
		if _, backupErr := os.Lstat(target.backupPath); backupErr == nil {
			return "conflicting (orphaned backup)"
		}
		return "absent"
	}
	raw, err := os.ReadFile(target.path)
	if err != nil {
		return "conflicting"
	}
	if !validManagedHook(raw) {
		if bytes.Contains(raw, []byte(managedHookMarker)) {
			return "modified"
		}
		return "conflicting"
	}
	if _, err := os.Lstat(target.backupPath); err == nil {
		return "installed (chained)"
	}
	return "installed"
}

func uninstallAutosnapHooks(ctx context.Context, out io.Writer) error {
	repoRoot, _, _, err := detectRepository(ctx)
	if err != nil {
		return err
	}
	hooksDir, _, _, err := resolveHooksDirectory(ctx, repoRoot)
	if err != nil {
		return err
	}
	targets := autosnapHookTargets(hooksDir)
	for _, target := range targets {
		exists, existsErr := hookPathExists(target.path)
		if existsErr != nil {
			return existsErr
		}
		if !exists {
			if _, backupErr := os.Lstat(target.backupPath); backupErr == nil {
				return fmt.Errorf("%s exists without a managed hook; refusing to remove it", target.backupPath)
			} else if !os.IsNotExist(backupErr) {
				return backupErr
			}
			continue
		}
		raw, readErr := os.ReadFile(target.path)
		if readErr != nil {
			return readErr
		}
		if !validManagedHook(raw) {
			return fmt.Errorf("%s is not an unmodified autosnap-managed hook; refusing to remove it", target.path)
		}
	}
	snapshots, err := snapshotHookPaths(targets)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if _, err := os.Lstat(target.path); os.IsNotExist(err) {
			continue
		}
		if err := os.Remove(target.path); err != nil {
			_ = restoreHookSnapshots(snapshots)
			return err
		}
		if _, err := os.Lstat(target.backupPath); err == nil {
			if err := os.Rename(target.backupPath, target.path); err != nil {
				_ = restoreHookSnapshots(snapshots)
				return err
			}
		} else if !os.IsNotExist(err) {
			_ = restoreHookSnapshots(snapshots)
			return err
		}
	}
	fmt.Fprintf(out, "autosnap hooks removed: %s\n", hooksDir)
	fmt.Fprintln(out, "running daemons were not stopped; use 'autosnap stop' in each worktree to stop them")
	return nil
}

func snapshotHookPaths(targets []hookTarget) ([]hookSnapshot, error) {
	paths := make([]string, 0, len(targets)*2)
	for _, target := range targets {
		paths = append(paths, target.path, target.backupPath)
	}
	snapshots := make([]hookSnapshot, 0, len(paths))
	for _, path := range paths {
		snapshot, err := snapshotHookPath(path)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func snapshotHookPath(path string) (hookSnapshot, error) {
	snapshot := hookSnapshot{path: path}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return snapshot, nil
	}
	if err != nil {
		return snapshot, err
	}
	snapshot.exists = true
	snapshot.mode = info.Mode()
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return snapshot, err
		}
		snapshot.symlink = target
		snapshot.isSymlink = true
		return snapshot, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return snapshot, err
	}
	snapshot.content = raw
	return snapshot, nil
}

func restoreHookSnapshots(snapshots []hookSnapshot) error {
	for _, snapshot := range snapshots {
		_ = os.Remove(snapshot.path)
		if !snapshot.exists {
			continue
		}
		if snapshot.isSymlink {
			if err := os.Symlink(snapshot.symlink, snapshot.path); err != nil {
				return err
			}
			continue
		}
		if err := writeFileAtomic(snapshot.path, snapshot.content, snapshot.mode.Perm()); err != nil {
			return err
		}
	}
	return nil
}
