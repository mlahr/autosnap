package autosnap

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestStartCommandWatchFlagValidation(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()

	cmd := newStartCommand()
	if err := cmd.Flags().Set("check", "true"); err != nil {
		t.Fatalf("set check flag failed: %v", err)
	}
	if err := cmd.Flags().Set("watch-mode", "bad"); err != nil {
		t.Fatalf("set watch-mode flag failed: %v", err)
	}
	_, _, err := resolveStartConfig(repo, cmd, "true", "", 60, snapshotModeBoth, commitModeCheckpoint, "bad", defaultPollInterval, defaultLogMaxBytes)
	if err == nil || !strings.Contains(err.Error(), "invalid --watch-mode") {
		t.Fatalf("expected invalid watch mode error, got %v", err)
	}

	cmd = newStartCommand()
	if err := cmd.Flags().Set("check", "true"); err != nil {
		t.Fatalf("set check flag failed: %v", err)
	}
	if err := cmd.Flags().Set("watch-mode", "poll"); err != nil {
		t.Fatalf("set watch-mode flag failed: %v", err)
	}
	if err := cmd.Flags().Set("poll-interval", "0s"); err != nil {
		t.Fatalf("set poll-interval flag failed: %v", err)
	}
	_, _, err = resolveStartConfig(repo, cmd, "true", "", 60, snapshotModeBoth, commitModeCheckpoint, watchModePoll, 0, defaultLogMaxBytes)
	if err == nil || !strings.Contains(err.Error(), "--poll-interval must be greater than 0") {
		t.Fatalf("expected invalid poll interval error, got %v", err)
	}
}

func TestLoadAutosnapConfig(t *testing.T) {
	repo := t.TempDir()
	cfg, found, err := loadAutosnapConfig(repo)
	if err != nil {
		t.Fatalf("load missing config failed: %v", err)
	}
	if found {
		t.Fatalf("expected missing config to report found=false")
	}
	if cfg.Check != "" {
		t.Fatalf("expected zero config for missing file, got %+v", cfg)
	}

	raw := []byte(`check = "go test ./..."
idle_seconds = 15
snapshot_mode = "staged"
commit_mode = "sync"
msg_source_cmd = "printf msg"
note_command = "printf note"
note_ref = "refs/notes/diffcog"
post_checkpoint_command = "printf post"
log_max_bytes = 2048

[watch]
mode = "auto"
poll_interval = "2s"
`)
	if err := os.WriteFile(autosnapConfigPath(repo), raw, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	cfg, found, err = loadAutosnapConfig(repo)
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if !found {
		t.Fatalf("expected config to be found")
	}
	if cfg.Check != "go test ./..." || cfg.IdleSeconds != 15 || cfg.SnapshotMode != snapshotModeStaged || cfg.CommitMode != commitModeSync || cfg.MsgSourceCmd != "printf msg" || cfg.NoteCommand != "printf note" || cfg.NoteRef != "refs/notes/diffcog" || cfg.PostCheckpointCommand != "printf post" || cfg.LogMaxBytes != 2048 || cfg.Watch.Mode != watchModeAuto || cfg.Watch.PollInterval != 2*time.Second {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestResolveStartConfigPrefersFlagsOverConfig(t *testing.T) {
	repo := t.TempDir()
	raw := []byte(`check = "go test ./..."
idle_seconds = 15
snapshot_mode = "staged"
commit_mode = "direct"
log_max_bytes = 4096

[watch]
mode = "poll"
poll_interval = "2s"
`)
	if err := os.WriteFile(autosnapConfigPath(repo), raw, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	cmd := newStartCommand()
	if err := cmd.Flags().Set("check", "make test"); err != nil {
		t.Fatalf("set check flag failed: %v", err)
	}
	if err := cmd.Flags().Set("idle", "30"); err != nil {
		t.Fatalf("set idle flag failed: %v", err)
	}
	if err := cmd.Flags().Set("watch-mode", "auto"); err != nil {
		t.Fatalf("set watch-mode flag failed: %v", err)
	}
	if err := cmd.Flags().Set("commit-mode", "checkpoint"); err != nil {
		t.Fatalf("set commit-mode flag failed: %v", err)
	}
	if err := cmd.Flags().Set("log-max-bytes", "8192"); err != nil {
		t.Fatalf("set log-max-bytes flag failed: %v", err)
	}
	if err := cmd.Flags().Set("note-command", "printf note"); err != nil {
		t.Fatalf("set note-command flag failed: %v", err)
	}
	if err := cmd.Flags().Set("note-ref", "refs/notes/diffcog"); err != nil {
		t.Fatalf("set note-ref flag failed: %v", err)
	}
	if err := cmd.Flags().Set("post-checkpoint-command", "printf post"); err != nil {
		t.Fatalf("set post-checkpoint-command flag failed: %v", err)
	}

	cfg, found, err := resolveStartConfig(repo, cmd, "make test", "", 30, snapshotModeBoth, commitModeCheckpoint, watchModeAuto, defaultPollInterval, 8192)
	if err != nil {
		t.Fatalf("resolve config failed: %v", err)
	}
	if !found {
		t.Fatalf("expected config to be found")
	}
	if cfg.Check != "make test" || cfg.IdleSeconds != 30 || cfg.CommitMode != commitModeCheckpoint || cfg.Watch.Mode != watchModeAuto || cfg.LogMaxBytes != 8192 || cfg.NoteCommand != "printf note" || cfg.NoteRef != "refs/notes/diffcog" || cfg.PostCheckpointCommand != "printf post" {
		t.Fatalf("expected flags to override config, got %+v", cfg)
	}
	if cfg.SnapshotMode != snapshotModeStaged || cfg.Watch.PollInterval != 2*time.Second {
		t.Fatalf("expected config values to remain for unset flags, got %+v", cfg)
	}
}

func TestResolveStartConfigValidatesNoteConfig(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(autosnapConfigPath(repo), []byte("check = \"true\"\nnote_command = \"printf note\"\n"), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	_, _, err := resolveStartConfig(repo, newStartCommand(), "", "", 60, snapshotModeBoth, commitModeCheckpoint, watchModeRecursive, defaultPollInterval, defaultLogMaxBytes)
	if err == nil || !strings.Contains(err.Error(), "note_ref is required when note_command is set") {
		t.Fatalf("expected missing note_ref error, got %v", err)
	}

	if err := os.WriteFile(autosnapConfigPath(repo), []byte("check = \"true\"\nnote_ref = \"refs/notes/diffcog\"\n"), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	_, _, err = resolveStartConfig(repo, newStartCommand(), "", "", 60, snapshotModeBoth, commitModeCheckpoint, watchModeRecursive, defaultPollInterval, defaultLogMaxBytes)
	if err == nil || !strings.Contains(err.Error(), "note_command is required when note_ref is set") {
		t.Fatalf("expected missing note_command error, got %v", err)
	}
}

func TestResolveStartConfigRejectsUnsafeDirectSnapshotMode(t *testing.T) {
	repo := t.TempDir()
	raw := []byte(`check = "go test ./..."
snapshot_mode = "staged"
commit_mode = "direct"
`)
	if err := os.WriteFile(autosnapConfigPath(repo), raw, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	_, _, err := resolveStartConfig(repo, newStartCommand(), "", "", 60, snapshotModeBoth, commitModeCheckpoint, watchModeRecursive, defaultPollInterval, defaultLogMaxBytes)
	if err == nil {
		t.Fatalf("expected unsafe direct snapshot mode to fail")
	}
	if !strings.Contains(err.Error(), "commit_mode direct requires snapshot_mode both") {
		t.Fatalf("expected direct snapshot mode error, got %v", err)
	}
}

func TestResolveStartConfigRejectsUnsafeSyncSnapshotMode(t *testing.T) {
	repo := t.TempDir()
	raw := []byte(`check = "go test ./..."
snapshot_mode = "staged"
commit_mode = "sync"
`)
	if err := os.WriteFile(autosnapConfigPath(repo), raw, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	_, _, err := resolveStartConfig(repo, newStartCommand(), "", "", 60, snapshotModeBoth, commitModeCheckpoint, watchModeRecursive, defaultPollInterval, defaultLogMaxBytes)
	if err == nil {
		t.Fatalf("expected unsafe sync snapshot mode to fail")
	}
	if !strings.Contains(err.Error(), "commit_mode sync requires snapshot_mode both") {
		t.Fatalf("expected sync snapshot mode error, got %v", err)
	}
}

func TestResolveStartConfigRejectsInvalidLogMaxBytes(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(autosnapConfigPath(repo), []byte("check = \"true\"\nlog_max_bytes = 0\n"), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	_, _, err := resolveStartConfig(repo, newStartCommand(), "", "", 60, snapshotModeBoth, commitModeCheckpoint, watchModeRecursive, defaultPollInterval, defaultLogMaxBytes)
	if err == nil || !strings.Contains(err.Error(), "log_max_bytes must be greater than 0") {
		t.Fatalf("expected invalid config log_max_bytes error, got %v", err)
	}

	cmd := newStartCommand()
	if err := cmd.Flags().Set("log-max-bytes", "0"); err != nil {
		t.Fatalf("set log-max-bytes flag failed: %v", err)
	}
	_, _, err = resolveStartConfig(repo, cmd, "", "", 60, snapshotModeBoth, commitModeCheckpoint, watchModeRecursive, defaultPollInterval, 0)
	if err == nil || !strings.Contains(err.Error(), "--log-max-bytes must be greater than 0") {
		t.Fatalf("expected invalid flag log-max-bytes error, got %v", err)
	}
}

func TestRootCommandRegistersRestart(t *testing.T) {
	root := NewRootCommand()
	for _, cmd := range root.Commands() {
		if cmd.Name() == "restart" {
			return
		}
	}
	t.Fatalf("expected root command to register restart")
}

func TestResolveRestartConfigReloadsConfigAndPreservesOriginalStartFlags(t *testing.T) {
	repo := t.TempDir()
	raw := []byte(`check = "go test ./..."
idle_seconds = 15
snapshot_mode = "both"
commit_mode = "direct"
msg_source_cmd = "printf config"
note_command = "printf config-note"
note_ref = "refs/notes/config"
log_max_bytes = 8192

[watch]
mode = "auto"
poll_interval = "4s"
`)
	if err := os.WriteFile(autosnapConfigPath(repo), raw, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	runState := autosnapRunState{
		CheckCommand:     "make verify",
		MsgSourceCmd:     "",
		NoteCommand:      "",
		NoteRef:          "",
		IdleSeconds:      45,
		SnapshotMode:     snapshotModeStaged,
		CommitMode:       commitModeCheckpoint,
		WatchMode:        watchModePoll,
		PollInterval:     2 * time.Second,
		LogMaxBytes:      4096,
		StartConfigFlags: []string{"check", "msg-source-cmd", "note-command", "note-ref", "idle"},
	}
	cfg, _, err := resolveRestartConfig(repo, runState)
	if err != nil {
		t.Fatalf("resolve restart config failed: %v", err)
	}
	if cfg.Check != "make verify" || cfg.MsgSourceCmd != "" || cfg.NoteCommand != "" || cfg.NoteRef != "" || cfg.IdleSeconds != 45 {
		t.Fatalf("expected original start flags to remain overrides, got %+v", cfg)
	}
	if cfg.SnapshotMode != snapshotModeBoth || cfg.CommitMode != commitModeDirect || cfg.Watch.Mode != watchModeAuto || cfg.Watch.PollInterval != 4*time.Second || cfg.LogMaxBytes != 8192 {
		t.Fatalf("expected unoverridden values from the current config, got %+v", cfg)
	}
}

func TestResolveRestartConfigWithoutOriginalFlagsUsesCurrentConfig(t *testing.T) {
	repo := t.TempDir()
	raw := []byte(`check = "go test ./..."
idle_seconds = 15

[watch]
mode = "poll"
`)
	if err := os.WriteFile(autosnapConfigPath(repo), raw, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	runState := autosnapRunState{
		CheckCommand:     "make verify",
		IdleSeconds:      45,
		StartConfigFlags: []string{},
	}
	cfg, _, err := resolveRestartConfig(repo, runState)
	if err != nil {
		t.Fatalf("resolve restart config failed: %v", err)
	}
	if cfg.Check != "go test ./..." || cfg.IdleSeconds != 15 || cfg.Watch.Mode != watchModePoll {
		t.Fatalf("expected current config to replace old run values, got %+v", cfg)
	}
}

func TestResolveRestartConfigPreservesEveryOriginalStartFlag(t *testing.T) {
	repo := t.TempDir()
	raw := []byte(`check = "printf config"
idle_seconds = 15
snapshot_mode = "working"
commit_mode = "checkpoint"
msg_source_cmd = "printf config-message"
note_command = "printf config-note"
note_ref = "refs/notes/config"
log_max_bytes = 8192
[watch]
mode = "auto"
poll_interval = "4s"
`)
	if err := os.WriteFile(autosnapConfigPath(repo), raw, 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	runState := autosnapRunState{
		CheckCommand:     "make verify",
		MsgSourceCmd:     "printf run-message",
		NoteCommand:      "printf run-note",
		NoteRef:          "refs/notes/run",
		IdleSeconds:      45,
		SnapshotMode:     snapshotModeBoth,
		CommitMode:       commitModeDirect,
		WatchMode:        watchModePoll,
		PollInterval:     2 * time.Second,
		LogMaxBytes:      4096,
		StartConfigFlags: append([]string{}, startConfigFlagNames...),
	}
	cfg, _, err := resolveRestartConfig(repo, runState)
	if err != nil {
		t.Fatalf("resolve restart config failed: %v", err)
	}
	if cfg.Check != runState.CheckCommand || cfg.MsgSourceCmd != runState.MsgSourceCmd || cfg.NoteCommand != runState.NoteCommand || cfg.NoteRef != runState.NoteRef || cfg.IdleSeconds != runState.IdleSeconds || cfg.SnapshotMode != runState.SnapshotMode || cfg.CommitMode != runState.CommitMode || cfg.Watch.Mode != runState.WatchMode || cfg.Watch.PollInterval != runState.PollInterval || cfg.LogMaxBytes != runState.LogMaxBytes {
		t.Fatalf("expected every original start flag value to remain effective, got %+v", cfg)
	}
}

func TestResolveRestartConfigRejectsInvalidRecordedFlag(t *testing.T) {
	_, _, err := resolveRestartConfig(t.TempDir(), autosnapRunState{StartConfigFlags: []string{"foreground"}})
	if err == nil || !strings.Contains(err.Error(), "unknown recorded start configuration flag") {
		t.Fatalf("expected invalid recorded flag error, got %v", err)
	}
}

func TestRestartCommandRejectsFlagsAndArguments(t *testing.T) {
	for _, args := range [][]string{{"--idle", "30"}, {"unexpected"}} {
		cmd := newRestartCommand()
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Fatalf("expected restart arguments %v to be rejected", args)
		}
	}
}

func TestWriteRestartLogAddsTimestampAndRestartPrefix(t *testing.T) {
	var buf bytes.Buffer
	if err := writeRestartLog(&buf, "configuration validated; idle_seconds=%d", 23); err != nil {
		t.Fatalf("write restart log failed: %v", err)
	}
	line := buf.String()
	if !strings.HasPrefix(line, "[") || !strings.Contains(line, "] restart: configuration validated; idle_seconds=23\n") {
		t.Fatalf("unexpected restart log line: %q", line)
	}
}

func TestFormatStartConfigFlags(t *testing.T) {
	if got := formatStartConfigFlags(nil); got != "none" {
		t.Fatalf("expected no preserved flags, got %q", got)
	}
	if got := formatStartConfigFlags([]string{"check", "idle"}); got != "--check,--idle" {
		t.Fatalf("unexpected preserved flag list: %q", got)
	}
}

func TestValidateRestartRunStateRequiresActiveProvenanceAwareDaemon(t *testing.T) {
	if err := validateRestartRunState(autosnapRunState{}); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("expected missing daemon error, got %v", err)
	}
	if err := validateRestartRunState(autosnapRunState{PID: 999999, StartConfigFlags: []string{}}); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("expected stale daemon error, got %v", err)
	}
	if err := validateRestartRunState(autosnapRunState{PID: os.Getpid()}); err == nil || !strings.Contains(err.Error(), "older autosnap version") {
		t.Fatalf("expected legacy run state error, got %v", err)
	}
}

func TestRunStateDistinguishesLegacyAndRecordedEmptyStartFlags(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "legacy.json")
	if err := os.WriteFile(legacyPath, []byte(`{"pid": 1}`), 0o644); err != nil {
		t.Fatalf("write legacy state failed: %v", err)
	}
	legacy, err := loadAutosnapRunState(legacyPath)
	if err != nil {
		t.Fatalf("load legacy state failed: %v", err)
	}
	if legacy.StartConfigFlags != nil {
		t.Fatalf("expected missing legacy startConfigFlags to decode as nil, got %#v", legacy.StartConfigFlags)
	}

	recordedPath := filepath.Join(dir, "recorded.json")
	if err := saveAutosnapRunState(recordedPath, autosnapRunState{PID: 1, StartConfigFlags: []string{}}); err != nil {
		t.Fatalf("save recorded state failed: %v", err)
	}
	recorded, err := loadAutosnapRunState(recordedPath)
	if err != nil {
		t.Fatalf("load recorded state failed: %v", err)
	}
	if recorded.StartConfigFlags == nil || len(recorded.StartConfigFlags) != 0 {
		t.Fatalf("expected recorded empty startConfigFlags, got %#v", recorded.StartConfigFlags)
	}
}

func TestResolveRestartConfigRejectsInvalidCurrentConfig(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(autosnapConfigPath(repo), []byte("check =\n"), 0o644); err != nil {
		t.Fatalf("write invalid config failed: %v", err)
	}
	_, _, err := resolveRestartConfig(repo, autosnapRunState{StartConfigFlags: []string{}})
	if err == nil || !strings.Contains(err.Error(), "parse ") {
		t.Fatalf("expected current config parse error, got %v", err)
	}
}

func TestRestartReloadsConfigAndPreservesOriginalStartFlags(t *testing.T) {
	requireIntegration(t)
	repo := createTestRepo(t)
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path failed")
	}
	projectRoot := filepath.Clean(filepath.Join(filepath.Dir(testFile), "..", ".."))
	binary := filepath.Join(t.TempDir(), "autosnap")
	build := exec.Command("go", "build", "-o", binary, "./cmd/autosnap")
	build.Dir = projectRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build autosnap failed: %v: %s", err, output)
	}

	writeConfig := func(raw string) {
		t.Helper()
		if err := os.WriteFile(autosnapConfigPath(repo), []byte(raw), 0o644); err != nil {
			t.Fatalf("write config failed: %v", err)
		}
	}
	run := func(args ...string) ([]byte, error) {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Dir = repo
		return cmd.CombinedOutput()
	}
	waitForState := func(predicate func(autosnapRunState) bool) autosnapRunState {
		t.Helper()
		runPath, err := runStatePath(repo)
		if err != nil {
			t.Fatalf("resolve run state path failed: %v", err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			state, loadErr := loadAutosnapRunState(runPath)
			if loadErr == nil && predicate(state) {
				return state
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("timed out waiting for daemon run state")
		return autosnapRunState{}
	}

	writeConfig(`check = "true"
idle_seconds = 60
[watch]
mode = "recursive"
poll_interval = "5s"
`)
	if output, err := run("start", "--idle", "23"); err != nil {
		t.Fatalf("start failed: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_, _ = run("stop")
	})
	initial := waitForState(func(state autosnapRunState) bool {
		return state.PID != 0 && state.IdleSeconds == 23
	})
	if !reflect.DeepEqual(initial.StartConfigFlags, []string{"idle"}) {
		t.Fatalf("expected original idle override provenance, got %#v", initial.StartConfigFlags)
	}

	writeConfig(`check = "printf reloaded"
idle_seconds = 9
[watch]
mode = "poll"
poll_interval = "2s"
`)
	if output, err := run("restart"); err != nil {
		t.Fatalf("restart failed: %v: %s", err, output)
	}
	restarted := waitForState(func(state autosnapRunState) bool {
		return state.PID != 0 && state.PID != initial.PID && state.CheckCommand == "printf reloaded"
	})
	if restarted.IdleSeconds != 23 || restarted.WatchMode != watchModePoll || restarted.PollInterval != 2*time.Second {
		t.Fatalf("expected current config plus original idle override, got %+v", restarted)
	}
	if !reflect.DeepEqual(restarted.StartConfigFlags, []string{"idle"}) {
		t.Fatalf("expected restart to retain original override provenance, got %#v", restarted.StartConfigFlags)
	}
	logPath, err := backgroundLogPath(repo)
	if err != nil {
		t.Fatalf("resolve log path failed: %v", err)
	}
	logRaw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read restart log failed: %v", err)
	}
	logOutput := string(logRaw)
	for _, want := range []string{
		"restart: restart requested; active_pid=",
		"restart: loading configuration; path=",
		"preserved_start_flags=--idle",
		"restart: configuration validated; source=",
		"idle_seconds=23",
		"restart: stopping daemon; pid=",
		"restart: daemon stopped; pid=",
		"restart: starting replacement daemon",
		"restart: replacement daemon started; pid=",
	} {
		if !strings.Contains(logOutput, want) {
			t.Fatalf("expected daemon log to contain %q, got:\n%s", want, logOutput)
		}
	}

	if output, err := run("restart", "--idle", "30"); err == nil {
		t.Fatalf("expected restart flag to be rejected, got output: %s", output)
	}
	unchanged := waitForState(func(state autosnapRunState) bool { return state.PID == restarted.PID })
	if !isAutosnapRunActive(unchanged) {
		t.Fatalf("expected rejected restart flag to leave daemon %d running", restarted.PID)
	}

	writeConfig("check =\n")
	if output, err := run("restart"); err == nil {
		t.Fatalf("expected invalid current config to reject restart, got output: %s", output)
	}
	unchanged = waitForState(func(state autosnapRunState) bool { return state.PID == restarted.PID })
	if !isAutosnapRunActive(unchanged) {
		t.Fatalf("expected invalid config to leave daemon %d running", restarted.PID)
	}
	logRaw, err = os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read restart failure log failed: %v", err)
	}
	if !strings.Contains(string(logRaw), "restart: configuration validation failed; error=") {
		t.Fatalf("expected invalid restart configuration to be logged, got:\n%s", logRaw)
	}
}

func TestConfigInitAndShowCommands(t *testing.T) {
	repo := t.TempDir()

	buf := &bytes.Buffer{}
	if err := writeDefaultAutosnapConfig(repo, buf, false); err != nil {
		t.Fatalf("config init failed: %v", err)
	}
	if _, err := os.Stat(autosnapConfigPath(repo)); err != nil {
		t.Fatalf("expected config file to exist: %v", err)
	}

	if err := writeDefaultAutosnapConfig(repo, io.Discard, false); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected init to refuse overwrite, got %v", err)
	}

	buf.Reset()
	if err := writeResolvedAutosnapConfig(repo, buf); err != nil {
		t.Fatalf("config show failed: %v", err)
	}
	expectedConfigPath := autosnapConfigPath(repo)
	out := buf.String()
	for _, want := range []string{
		"path: " + expectedConfigPath,
		"exists: true",
		"check: make test",
		"commit_mode: checkpoint",
		"note_command: ",
		"note_ref: ",
		"post_checkpoint_command: ",
		"log_max_bytes: 10485760",
		"watch.mode: recursive",
		"watch.poll_interval: 5s",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected config show output to contain %q, got %q", want, out)
		}
	}
}

func TestStartCommandAcceptsConfigCheck(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(autosnapConfigPath(repo), []byte("check = \"true\"\nidle_seconds = 1\n"), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	cmd := newStartCommand()
	if err := cmd.Flags().Set("watch-mode", "bad"); err != nil {
		t.Fatalf("set watch-mode flag failed: %v", err)
	}
	_, _, err := resolveStartConfig(repo, cmd, "", "", 60, snapshotModeBoth, commitModeCheckpoint, "bad", defaultPollInterval, defaultLogMaxBytes)
	if err == nil || !strings.Contains(err.Error(), "invalid --watch-mode") {
		t.Fatalf("expected invalid watch mode error after config-provided check, got %v", err)
	}
}

func TestStartDetachedArgsForwardWatchOptions(t *testing.T) {
	args := startDetachedArgs("/bin/autosnap", "make build", "printf msg", "printf note", "refs/notes/diffcog", "printf post", 30, snapshotModeBoth, commitModeCheckpoint, watchModeAuto, 2*time.Second, 4096, "token", []string{"check", "idle"})
	joined := strings.Join(args, "\n")
	for _, want := range []string{
		"--resolved-config",
		"--start-config-flags\ncheck,idle",
		"--watch-mode\nauto",
		"--poll-interval\n2s",
		"--commit-mode\ncheckpoint",
		"--log-max-bytes\n4096",
		"--msg-source-cmd\nprintf msg",
		"--note-command\nprintf note",
		"--note-ref\nrefs/notes/diffcog",
		"--post-checkpoint-command\nprintf post",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected detached args to contain %q, got %v", want, args)
		}
	}
}

func TestStartDetachedArgsForwardEmptyOptionalValues(t *testing.T) {
	args := startDetachedArgs("/bin/autosnap", "make build", "", "", "", "", 30, snapshotModeBoth, commitModeCheckpoint, watchModeRecursive, defaultPollInterval, defaultLogMaxBytes, "token", []string{})
	for _, name := range []string{"--msg-source-cmd", "--note-command", "--note-ref", "--post-checkpoint-command"} {
		index := slices.Index(args, name)
		if index < 0 || index+1 >= len(args) || args[index+1] != "" {
			t.Fatalf("expected %s to forward an explicit empty value, got %v", name, args)
		}
	}
}

func TestWatchModeHelpers(t *testing.T) {
	if mode, err := normalizeWatchMode(""); err != nil || mode != watchModeRecursive {
		t.Fatalf("expected empty watch mode to normalize to recursive, got %q err=%v", mode, err)
	}
	if _, err := normalizeWatchMode("bad"); err == nil {
		t.Fatalf("expected invalid watch mode error")
	}
	if isWatchLimitError(os.ErrInvalid) {
		t.Fatalf("did not expect os.ErrInvalid to be recognized as watch limit")
	}
	if isWatchLimitError(nil) {
		t.Fatalf("did not expect nil to be recognized as watch limit")
	}
	if !isWatchLimitError(&os.PathError{Op: "open", Path: "x", Err: syscall.EMFILE}) {
		t.Fatalf("expected EMFILE to be recognized as watch limit")
	}
}
