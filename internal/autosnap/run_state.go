package autosnap

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var readProcessCommandLine = defaultReadProcessCommandLine

type autosnapRunState struct {
	PID                   int           `json:"pid"`
	RepoRoot              string        `json:"repoRoot"`
	BranchRef             string        `json:"branchRef"`
	BranchDisplay         string        `json:"branchDisplay"`
	CheckCommand          string        `json:"checkCommand"`
	MsgSourceCmd          string        `json:"msgSourceCmd"`
	MsgSourceCmdSet       bool          `json:"msgSourceCmdSet,omitempty"`
	NoteCommand           string        `json:"noteCommand,omitempty"`
	NoteRef               string        `json:"noteRef,omitempty"`
	PostCheckpointCommand string        `json:"postCheckpointCommand,omitempty"`
	SnapshotMode          string        `json:"snapshotMode"`
	CommitMode            string        `json:"commitMode"`
	WatchMode             string        `json:"watchMode"`
	PollInterval          time.Duration `json:"pollInterval"`
	LogMaxBytes           int64         `json:"logMaxBytes"`
	StartConfigFlags      []string      `json:"startConfigFlags"`
	RunToken              string        `json:"runToken"`
	IdleSeconds           int           `json:"idleSeconds"`
	StartedAt             string        `json:"startedAt"`
}

func runStatePath(repoRoot string) (string, error) {
	statePath, err := stateFilePath(repoRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(statePath), "run.json"), nil
}

func loadAutosnapRunState(path string) (autosnapRunState, error) {
	var state autosnapRunState
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, err
	}

	if err := json.Unmarshal(raw, &state); err != nil {
		return state, err
	}
	return state, nil
}

func saveAutosnapRunState(path string, state autosnapRunState) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, raw, 0o644)
}

func removeAutosnapRunState(path string) error {
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return nil
}

func ensureNoActiveRunForRepo(repoRoot string) error {
	path, err := runStatePath(repoRoot)
	if err != nil {
		return err
	}

	state, err := loadAutosnapRunState(path)
	if err != nil {
		return err
	}

	if state.PID == 0 {
		return removeAutosnapRunState(path)
	}

	if isAutosnapRunActive(state) {
		return fmt.Errorf("autosnap already running (pid=%d)", state.PID)
	}

	return removeAutosnapRunState(path)
}

func isAutosnapRunActive(state autosnapRunState) bool {
	if state.PID == 0 {
		return false
	}

	if !isProcessAlive(state.PID) {
		return false
	}

	if state.RunToken == "" {
		return true
	}

	cmdLine, err := readProcessCommandLine(state.PID)
	if err != nil {
		return true
	}

	return isAutosnapCommandLine(cmdLine, state.RunToken)
}

func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	err = process.Signal(syscall.Signal(0))
	if runtime.GOOS == "windows" {
		// Some Windows configurations treat signal 0 as unsupported.
		return err == nil
	}

	return err == nil
}

func isAutosnapCommandLine(commandLine, runToken string) bool {
	if !strings.Contains(commandLine, "--daemon") {
		return false
	}

	fields := strings.Fields(commandLine)
	if len(fields) == 0 {
		return false
	}
	execName := filepath.Base(fields[0])
	if execName != "autosnap" {
		// Fallback for historical command line formats where the binary path includes
		// the extension or additional path components.
		if !strings.HasSuffix(execName, "autosnap") {
			return false
		}
	}

	sawStart := false
	for _, field := range fields {
		if field == "start" {
			sawStart = true
			break
		}
	}
	if !sawStart {
		return false
	}

	if runToken == "" {
		return true
	}

	for i := 0; i < len(fields); i++ {
		if fields[i] == "--run-token" {
			if i+1 < len(fields) && fields[i+1] == runToken {
				return true
			}
			continue
		}
		if strings.HasPrefix(fields[i], "--run-token=") && strings.TrimPrefix(fields[i], "--run-token=") == runToken {
			return true
		}
	}

	// Legacy behavior used to omit --run-token in some flows.
	if !strings.Contains(commandLine, "--run-token") {
		return true
	}

	return false
}

func defaultReadProcessCommandLine(pid int) (string, error) {
	cmd := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("failed to read command line for pid=%d: %s", pid, strings.TrimSpace(stderr.String()))
		}
		return "", fmt.Errorf("failed to read command line for pid=%d", pid)
	}

	return strings.TrimSpace(stdout.String()), nil
}

func newRunToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
