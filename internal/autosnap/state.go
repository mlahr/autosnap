package autosnap

import (
	"encoding/json"
	"errors"
	"os"
)

type autosnapState struct {
	RepoRoot            string `json:"repoRoot"`
	LastBranch          string `json:"lastBranch"`
	LastCheckpointRef   string `json:"lastCheckpointRef"`
	LastCheckpointAt    string `json:"lastCheckpointAt"`
	LastCheckpointTree  string `json:"lastCheckpointTree"`
	LastCheckAt         string `json:"lastCheckAt"`
	LastCheckStatus     string `json:"lastCheckStatus"`
	LastCheckCommand    string `json:"lastCheckCommand"`
	LastCheckDurationMs int64  `json:"lastCheckDurationMs"`
	LastFailureAt       string `json:"lastFailureAt"`
	LastFailureExitCode int    `json:"lastFailureExitCode"`
}

func loadAutosnapState(path string) (autosnapState, error) {
	var state autosnapState

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state, nil
		}
		return state, err
	}

	if err := json.Unmarshal(raw, &state); err != nil {
		return state, err
	}

	return state, nil
}

func saveAutosnapState(path string, state autosnapState) error {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, raw, 0o644)
}
