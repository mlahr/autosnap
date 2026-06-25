package autosnap

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

func newConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage autosnap configuration",
	}

	cmd.AddCommand(newConfigInitCommand())
	cmd.AddCommand(newConfigShowCommand())
	return cmd
}

func newConfigInitCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a repo-local .autosnap.toml",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			repoRoot, _, _, err := detectRepository(context.Background())
			if err != nil {
				return err
			}

			return writeDefaultAutosnapConfig(repoRoot, out, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Overwrite existing config")
	return cmd
}

func newConfigShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show resolved autosnap configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			repoRoot, _, _, err := detectRepository(context.Background())
			if err != nil {
				return err
			}

			return writeResolvedAutosnapConfig(repoRoot, out)
		},
	}
}

func writeDefaultAutosnapConfig(repoRoot string, out io.Writer, force bool) error {
	path := autosnapConfigPath(repoRoot)
	if !force {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists (use --force to overwrite)", autosnapConfigFileName)
		} else if !os.IsNotExist(err) {
			return err
		}
	}

	if err := writeFileAtomic(path, defaultAutosnapConfigTemplate(), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "created config: %s\n", path)
	return nil
}

func writeResolvedAutosnapConfig(repoRoot string, out io.Writer) error {
	cfg := defaultAutosnapConfig()
	fileCfg, found, err := loadAutosnapConfig(repoRoot)
	if err != nil {
		return err
	}
	if found {
		mergeAutosnapConfig(&cfg, fileCfg)
	}

	fmt.Fprintf(out, "path: %s\n", autosnapConfigPath(repoRoot))
	fmt.Fprintf(out, "exists: %t\n", found)
	fmt.Fprintf(out, "check: %s\n", cfg.Check)
	fmt.Fprintf(out, "idle_seconds: %d\n", cfg.IdleSeconds)
	fmt.Fprintf(out, "snapshot_mode: %s\n", cfg.SnapshotMode)
	fmt.Fprintf(out, "commit_mode: %s\n", cfg.CommitMode)
	fmt.Fprintf(out, "msg_source_cmd: %s\n", cfg.MsgSourceCmd)
	fmt.Fprintf(out, "note_command: %s\n", cfg.NoteCommand)
	fmt.Fprintf(out, "note_ref: %s\n", cfg.NoteRef)
	fmt.Fprintf(out, "log_max_bytes: %d\n", cfg.LogMaxBytes)
	fmt.Fprintf(out, "watch.mode: %s\n", cfg.Watch.Mode)
	fmt.Fprintf(out, "watch.poll_interval: %s\n", cfg.Watch.PollInterval)
	return nil
}
