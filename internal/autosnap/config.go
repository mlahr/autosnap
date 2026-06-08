package autosnap

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/spf13/cobra"
)

const autosnapConfigFileName = ".autosnap.toml"

type autosnapConfig struct {
	Check        string              `toml:"check"`
	IdleSeconds  int                 `toml:"idle_seconds"`
	SnapshotMode string              `toml:"snapshot_mode"`
	CommitMode   string              `toml:"commit_mode"`
	MsgSourceCmd string              `toml:"msg_source_cmd"`
	Watch        autosnapWatchConfig `toml:"watch"`
}

type autosnapWatchConfig struct {
	Mode         string        `toml:"mode"`
	PollInterval time.Duration `toml:"poll_interval"`
}

func defaultAutosnapConfig() autosnapConfig {
	return autosnapConfig{
		IdleSeconds:  60,
		SnapshotMode: snapshotModeBoth,
		CommitMode:   commitModeCheckpoint,
		Watch: autosnapWatchConfig{
			Mode:         watchModeRecursive,
			PollInterval: defaultPollInterval,
		},
	}
}

func autosnapConfigPath(repoRoot string) string {
	return filepath.Join(repoRoot, autosnapConfigFileName)
}

func loadAutosnapConfig(repoRoot string) (autosnapConfig, bool, error) {
	cfg := autosnapConfig{}
	path := autosnapConfigPath(repoRoot)
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, false, nil
		}
		return cfg, false, err
	}
	if _, err := toml.Decode(string(raw), &cfg); err != nil {
		return cfg, true, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, true, nil
}

func resolveStartConfig(repoRoot string, cmd *cobra.Command, checkCommand, msgSourceCmd string, idleSeconds int, snapshotMode, commitMode, watchMode string, pollInterval time.Duration) (autosnapConfig, bool, error) {
	cfg := defaultAutosnapConfig()
	fileCfg, found, err := loadAutosnapConfig(repoRoot)
	if err != nil {
		return cfg, found, err
	}

	if found {
		mergeAutosnapConfig(&cfg, fileCfg)
	}

	flags := cmd.Flags()
	if flags.Changed("check") {
		cfg.Check = checkCommand
	}
	if flags.Changed("msg-source-cmd") {
		cfg.MsgSourceCmd = msgSourceCmd
	}
	if flags.Changed("idle") {
		cfg.IdleSeconds = idleSeconds
	}
	if flags.Changed("snapshot-mode") {
		cfg.SnapshotMode = snapshotMode
	}
	if flags.Changed("commit-mode") {
		cfg.CommitMode = commitMode
	}
	if flags.Changed("watch-mode") {
		cfg.Watch.Mode = watchMode
	}
	if flags.Changed("poll-interval") {
		cfg.Watch.PollInterval = pollInterval
	}

	cfg.Check = strings.TrimSpace(cfg.Check)
	cfg.MsgSourceCmd = strings.TrimSpace(cfg.MsgSourceCmd)
	cfg.SnapshotMode = strings.TrimSpace(cfg.SnapshotMode)
	cfg.CommitMode = strings.TrimSpace(cfg.CommitMode)
	cfg.Watch.Mode = strings.TrimSpace(cfg.Watch.Mode)

	if err := validateStartConfig(cfg, flags.Changed("idle"), flags.Changed("poll-interval")); err != nil {
		return cfg, found, err
	}

	normalizedSnapshotMode, err := normalizeSnapshotMode(cfg.SnapshotMode)
	if err != nil {
		if flags.Changed("snapshot-mode") {
			return cfg, found, fmt.Errorf("invalid --snapshot-mode %q (expected both, staged, working)", cfg.SnapshotMode)
		}
		return cfg, found, fmt.Errorf("invalid snapshot_mode %q (expected both, staged, working)", cfg.SnapshotMode)
	}
	cfg.SnapshotMode = normalizedSnapshotMode

	normalizedCommitMode, err := normalizeCommitMode(cfg.CommitMode)
	if err != nil {
		if flags.Changed("commit-mode") {
			return cfg, found, fmt.Errorf("invalid --commit-mode %q (expected checkpoint, direct, sync)", cfg.CommitMode)
		}
		return cfg, found, fmt.Errorf("invalid commit_mode %q (expected checkpoint, direct, sync)", cfg.CommitMode)
	}
	cfg.CommitMode = normalizedCommitMode
	if isDirectCommitMode(cfg.CommitMode) && cfg.SnapshotMode != snapshotModeBoth {
		return cfg, found, fmt.Errorf("commit_mode %s requires snapshot_mode both", cfg.CommitMode)
	}

	normalizedWatchMode, err := normalizeWatchMode(cfg.Watch.Mode)
	if err != nil {
		if flags.Changed("watch-mode") {
			return cfg, found, fmt.Errorf("invalid --watch-mode %q (expected recursive, poll, auto)", cfg.Watch.Mode)
		}
		return cfg, found, fmt.Errorf("invalid watch.mode %q (expected recursive, poll, auto)", cfg.Watch.Mode)
	}
	cfg.Watch.Mode = normalizedWatchMode

	return cfg, found, nil
}

func mergeAutosnapConfig(dst *autosnapConfig, src autosnapConfig) {
	if src.Check != "" {
		dst.Check = src.Check
	}
	if src.IdleSeconds != 0 {
		dst.IdleSeconds = src.IdleSeconds
	}
	if src.SnapshotMode != "" {
		dst.SnapshotMode = src.SnapshotMode
	}
	if src.CommitMode != "" {
		dst.CommitMode = src.CommitMode
	}
	if src.MsgSourceCmd != "" {
		dst.MsgSourceCmd = src.MsgSourceCmd
	}
	if src.Watch.Mode != "" {
		dst.Watch.Mode = src.Watch.Mode
	}
	if src.Watch.PollInterval != 0 {
		dst.Watch.PollInterval = src.Watch.PollInterval
	}
}

func validateStartConfig(cfg autosnapConfig, idleFromFlag bool, pollIntervalFromFlag bool) error {
	if cfg.Check == "" {
		return errors.New("--check is required (or set check in .autosnap.toml)")
	}
	if cfg.IdleSeconds <= 0 {
		if idleFromFlag {
			return errors.New("--idle must be greater than 0")
		}
		return errors.New("idle_seconds must be greater than 0")
	}
	if cfg.Watch.PollInterval <= 0 {
		if pollIntervalFromFlag {
			return errors.New("--poll-interval must be greater than 0")
		}
		return errors.New("watch.poll_interval must be greater than 0")
	}
	return nil
}

func encodeAutosnapConfig(cfg autosnapConfig) ([]byte, error) {
	var buf bytes.Buffer
	encoder := toml.NewEncoder(&buf)
	if err := encoder.Encode(cfg); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func defaultAutosnapConfigTemplate() []byte {
	return []byte(`check = "npm test"
idle_seconds = 60
snapshot_mode = "both"
commit_mode = "checkpoint"
msg_source_cmd = ""

[watch]
mode = "recursive"
poll_interval = "5s"
`)
}
