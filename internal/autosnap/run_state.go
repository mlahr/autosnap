package autosnap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

type autosnapRunState struct {
	PID           int    `json:"pid"`
	RepoRoot      string `json:"repoRoot"`
	BranchRef     string `json:"branchRef"`
	BranchDisplay string `json:"branchDisplay"`
	CheckCommand  string `json:"checkCommand"`
	IdleSeconds   int    `json:"idleSeconds"`
	StartedAt     string `json:"startedAt"`
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

	if isProcessAlive(state.PID) {
		return fmt.Errorf("autosnap already running (pid=%d)", state.PID)
	}

	return removeAutosnapRunState(path)
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
