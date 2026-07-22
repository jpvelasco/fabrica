package state

import (
	"encoding/json"
	"fmt"
	"os"
)

const stateFile = ".fabrica/state.json"

// ReadStateOrNew reads state from the local cache file (.fabrica/state.json).
// If the file does not exist, it returns a fresh empty state initialised with
// the provided account and region — the caller should pass empty strings if
// those values are not yet known.
func ReadStateOrNew(account, region string) (*State, error) {
	data, err := os.ReadFile(stateFile)
	if os.IsNotExist(err) {
		return NewState(account, region), nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading state file: %w", err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parsing state file: %w", err)
	}
	return &st, nil
}

// WriteState persists state to the local cache file (.fabrica/state.json).
func WriteState(st *State) error {
	// #nosec G301 -- directory needs execute for traversal
	if err := os.MkdirAll(".fabrica", 0700); err != nil {
		return fmt.Errorf("creating .fabrica directory: %w", err)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("serializing state: %w", err)
	}
	return os.WriteFile(stateFile, data, 0600)
}
