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

const (
	autosnapConfigFileName = ".autosnap.toml"
	defaultLogMaxBytes     = int64(10 * 1024 * 1024)
)

type autosnapConfig struct {
	Check                 string              `toml:"check"`
	IdleSeconds           int                 `toml:"idle_seconds"`
	SnapshotMode          string              `toml:"snapshot_mode"`
	CommitMode            string              `toml:"commit_mode"`
	MsgSourceCmd          string              `toml:"msg_source_cmd"`
	MsgBodySourceCmd      string              `toml:"msg_body_source_cmd"`
	NoteCommand           string              `toml:"note_command"`
	NoteRef               string              `toml:"note_ref"`
	PostCheckpointCommand string              `toml:"post_checkpoint_command"`
	LogMaxBytes           int64               `toml:"log_max_bytes"`
	ReadyTimeout          time.Duration       `toml:"ready_timeout"`
	Watch                 autosnapWatchConfig `toml:"watch"`

	logMaxBytesSet  bool
	readyTimeoutSet bool
}

type autosnapWatchConfig struct {
	Mode         string        `toml:"mode"`
	PollInterval time.Duration `toml:"poll_interval"`
}

var startConfigFlagNames = []string{
	"check",
	"msg-source-cmd",
	"msg-body-source-cmd",
	"note-command",
	"note-ref",
	"post-checkpoint-command",
	"idle",
	"snapshot-mode",
	"commit-mode",
	"watch-mode",
	"poll-interval",
	"log-max-bytes",
}

type autosnapConfigOverrides struct {
	values autosnapConfig
	set    map[string]bool
}

func defaultAutosnapConfig() autosnapConfig {
	return autosnapConfig{
		IdleSeconds:  60,
		SnapshotMode: snapshotModeBoth,
		CommitMode:   commitModeCheckpoint,
		LogMaxBytes:  defaultLogMaxBytes,
		ReadyTimeout: defaultDaemonReadyTimeout,
		Watch: autosnapWatchConfig{
			Mode:         watchModeRecursive,
			PollInterval: defaultPollInterval,
		},
	}
}

func autosnapConfigPath(repoRoot string) string {
	return filepath.Join(repoRoot, autosnapConfigFileName)
}

func noteCommandFlag(cmd *cobra.Command) string {
	value, _ := cmd.Flags().GetString("note-command")
	return value
}

func noteRefFlag(cmd *cobra.Command) string {
	value, _ := cmd.Flags().GetString("note-ref")
	return value
}

func postCheckpointCommandFlag(cmd *cobra.Command) string {
	value, _ := cmd.Flags().GetString("post-checkpoint-command")
	return value
}

func msgBodySourceCmdFlag(cmd *cobra.Command) string {
	value, _ := cmd.Flags().GetString("msg-body-source-cmd")
	return value
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
	meta, err := toml.Decode(string(raw), &cfg)
	if err != nil {
		return cfg, true, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.logMaxBytesSet = meta.IsDefined("log_max_bytes")
	cfg.readyTimeoutSet = meta.IsDefined("ready_timeout")
	return cfg, true, nil
}

func resolveStartConfig(repoRoot string, cmd *cobra.Command, checkCommand, msgSourceCmd string, idleSeconds int, snapshotMode, commitMode, watchMode string, pollInterval time.Duration, logMaxBytes int64) (autosnapConfig, bool, error) {
	return resolveStartConfigWithBody(repoRoot, cmd, checkCommand, msgSourceCmd, msgBodySourceCmdFlag(cmd), idleSeconds, snapshotMode, commitMode, watchMode, pollInterval, logMaxBytes)
}

func resolveStartConfigWithBody(repoRoot string, cmd *cobra.Command, checkCommand, msgSourceCmd, msgBodySourceCmd string, idleSeconds int, snapshotMode, commitMode, watchMode string, pollInterval time.Duration, logMaxBytes int64) (autosnapConfig, bool, error) {
	return resolveStartConfigWithFileAndBody(repoRoot, cmd, checkCommand, msgSourceCmd, msgBodySourceCmd, idleSeconds, snapshotMode, commitMode, watchMode, pollInterval, logMaxBytes, true)
}

func resolveStartConfigWithFile(repoRoot string, cmd *cobra.Command, checkCommand, msgSourceCmd string, idleSeconds int, snapshotMode, commitMode, watchMode string, pollInterval time.Duration, logMaxBytes int64, loadFile bool) (autosnapConfig, bool, error) {
	return resolveStartConfigWithFileAndBody(repoRoot, cmd, checkCommand, msgSourceCmd, msgBodySourceCmdFlag(cmd), idleSeconds, snapshotMode, commitMode, watchMode, pollInterval, logMaxBytes, loadFile)
}

func resolveStartConfigWithFileAndBody(repoRoot string, cmd *cobra.Command, checkCommand, msgSourceCmd, msgBodySourceCmd string, idleSeconds int, snapshotMode, commitMode, watchMode string, pollInterval time.Duration, logMaxBytes int64, loadFile bool) (autosnapConfig, bool, error) {
	overrides := autosnapConfigOverrides{
		values: autosnapConfig{
			Check:                 checkCommand,
			MsgSourceCmd:          msgSourceCmd,
			MsgBodySourceCmd:      msgBodySourceCmd,
			NoteCommand:           noteCommandFlag(cmd),
			NoteRef:               noteRefFlag(cmd),
			PostCheckpointCommand: postCheckpointCommandFlag(cmd),
			IdleSeconds:           idleSeconds,
			SnapshotMode:          snapshotMode,
			CommitMode:            commitMode,
			LogMaxBytes:           logMaxBytes,
			Watch: autosnapWatchConfig{
				Mode:         watchMode,
				PollInterval: pollInterval,
			},
		},
		set: changedConfigFlags(cmd),
	}
	return resolveAutosnapConfig(repoRoot, overrides, loadFile)
}

func resolveAutosnapConfig(repoRoot string, overrides autosnapConfigOverrides, loadFile bool) (autosnapConfig, bool, error) {
	cfg := defaultAutosnapConfig()
	found := false
	if loadFile {
		fileCfg, configFound, err := loadAutosnapConfig(repoRoot)
		if err != nil {
			return cfg, configFound, err
		}
		found = configFound
		if found {
			mergeAutosnapConfig(&cfg, fileCfg)
		}
	}

	if overrides.set["check"] {
		cfg.Check = overrides.values.Check
	}
	if overrides.set["msg-source-cmd"] {
		cfg.MsgSourceCmd = overrides.values.MsgSourceCmd
	}
	if overrides.set["msg-body-source-cmd"] {
		cfg.MsgBodySourceCmd = overrides.values.MsgBodySourceCmd
	}
	if overrides.set["note-command"] {
		cfg.NoteCommand = overrides.values.NoteCommand
	}
	if overrides.set["note-ref"] {
		cfg.NoteRef = overrides.values.NoteRef
	}
	if overrides.set["post-checkpoint-command"] {
		cfg.PostCheckpointCommand = overrides.values.PostCheckpointCommand
	}
	if overrides.set["idle"] {
		cfg.IdleSeconds = overrides.values.IdleSeconds
	}
	if overrides.set["snapshot-mode"] {
		cfg.SnapshotMode = overrides.values.SnapshotMode
	}
	if overrides.set["commit-mode"] {
		cfg.CommitMode = overrides.values.CommitMode
	}
	if overrides.set["watch-mode"] {
		cfg.Watch.Mode = overrides.values.Watch.Mode
	}
	if overrides.set["poll-interval"] {
		cfg.Watch.PollInterval = overrides.values.Watch.PollInterval
	}
	if overrides.set["log-max-bytes"] {
		cfg.LogMaxBytes = overrides.values.LogMaxBytes
	}

	cfg.Check = strings.TrimSpace(cfg.Check)
	cfg.MsgSourceCmd = strings.TrimSpace(cfg.MsgSourceCmd)
	cfg.MsgBodySourceCmd = strings.TrimSpace(cfg.MsgBodySourceCmd)
	cfg.NoteCommand = strings.TrimSpace(cfg.NoteCommand)
	cfg.NoteRef = strings.TrimSpace(cfg.NoteRef)
	cfg.PostCheckpointCommand = strings.TrimSpace(cfg.PostCheckpointCommand)
	cfg.SnapshotMode = strings.TrimSpace(cfg.SnapshotMode)
	cfg.CommitMode = strings.TrimSpace(cfg.CommitMode)
	cfg.Watch.Mode = strings.TrimSpace(cfg.Watch.Mode)

	if err := validateStartConfig(cfg, overrides.set["idle"], overrides.set["poll-interval"], overrides.set["log-max-bytes"]); err != nil {
		return cfg, found, err
	}

	normalizedSnapshotMode, err := normalizeSnapshotMode(cfg.SnapshotMode)
	if err != nil {
		if overrides.set["snapshot-mode"] {
			return cfg, found, fmt.Errorf("invalid --snapshot-mode %q (expected both, staged, working)", cfg.SnapshotMode)
		}
		return cfg, found, fmt.Errorf("invalid snapshot_mode %q (expected both, staged, working)", cfg.SnapshotMode)
	}
	cfg.SnapshotMode = normalizedSnapshotMode

	normalizedCommitMode, err := normalizeCommitMode(cfg.CommitMode)
	if err != nil {
		if overrides.set["commit-mode"] {
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
		if overrides.set["watch-mode"] {
			return cfg, found, fmt.Errorf("invalid --watch-mode %q (expected recursive, poll, auto)", cfg.Watch.Mode)
		}
		return cfg, found, fmt.Errorf("invalid watch.mode %q (expected recursive, poll, auto)", cfg.Watch.Mode)
	}
	cfg.Watch.Mode = normalizedWatchMode

	return cfg, found, nil
}

func changedConfigFlags(cmd *cobra.Command) map[string]bool {
	changed := make(map[string]bool)
	for _, name := range startConfigFlagNames {
		if cmd.Flags().Changed(name) {
			changed[name] = true
		}
	}
	return changed
}

func configFlagNames(cmd *cobra.Command) []string {
	changed := changedConfigFlags(cmd)
	names := make([]string, 0, len(changed))
	for _, name := range startConfigFlagNames {
		if changed[name] {
			names = append(names, name)
		}
	}
	return names
}

func configFlagSet(names []string) (map[string]bool, error) {
	allowed := make(map[string]bool, len(startConfigFlagNames))
	for _, name := range startConfigFlagNames {
		allowed[name] = true
	}

	set := make(map[string]bool, len(names))
	for _, name := range names {
		if !allowed[name] {
			return nil, fmt.Errorf("unknown recorded start configuration flag %q", name)
		}
		if set[name] {
			return nil, fmt.Errorf("duplicate recorded start configuration flag %q", name)
		}
		set[name] = true
	}
	return set, nil
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
	if src.MsgBodySourceCmd != "" {
		dst.MsgBodySourceCmd = src.MsgBodySourceCmd
	}
	if src.NoteCommand != "" {
		dst.NoteCommand = src.NoteCommand
	}
	if src.NoteRef != "" {
		dst.NoteRef = src.NoteRef
	}
	if src.PostCheckpointCommand != "" {
		dst.PostCheckpointCommand = src.PostCheckpointCommand
	}
	if src.logMaxBytesSet {
		dst.LogMaxBytes = src.LogMaxBytes
	}
	if src.readyTimeoutSet {
		dst.ReadyTimeout = src.ReadyTimeout
		dst.readyTimeoutSet = true
	}
	if src.Watch.Mode != "" {
		dst.Watch.Mode = src.Watch.Mode
	}
	if src.Watch.PollInterval != 0 {
		dst.Watch.PollInterval = src.Watch.PollInterval
	}
}

func validateStartConfig(cfg autosnapConfig, idleFromFlag bool, pollIntervalFromFlag bool, logMaxBytesFromFlag bool) error {
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
	if cfg.LogMaxBytes <= 0 {
		if logMaxBytesFromFlag {
			return errors.New("--log-max-bytes must be greater than 0")
		}
		return errors.New("log_max_bytes must be greater than 0")
	}
	if cfg.ReadyTimeout <= 0 {
		return errors.New("ready_timeout must be greater than 0")
	}
	if cfg.NoteCommand != "" && cfg.NoteRef == "" {
		return errors.New("note_ref is required when note_command is set")
	}
	if cfg.NoteCommand == "" && cfg.NoteRef != "" {
		return errors.New("note_command is required when note_ref is set")
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
	return []byte(`check = "make test"
idle_seconds = 60
snapshot_mode = "both"
commit_mode = "checkpoint"
msg_source_cmd = ""
msg_body_source_cmd = ""
note_command = ""
note_ref = ""
post_checkpoint_command = ""
log_max_bytes = 10485760
ready_timeout = "30s"

[watch]
mode = "recursive"
poll_interval = "5s"
`)
}
