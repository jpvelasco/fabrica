package state

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/jpvelasco/fabrica/internal/oplog"
)

const stateFile = ".fabrica/state.json"

// ReadStateOrNew reads state from the local cache file (.fabrica/state.json).
// If the file does not exist, it returns a fresh empty state initialised with
// the provided account and region — the caller should pass empty strings if
// those values are not yet known.
func ReadStateOrNew(account, region string) (*State, error) {
	data, err := os.ReadFile(stateFile)
	if os.IsNotExist(err) {
		oplog.Logger().Debug("state file not found, creating new state", "file", stateFile)
		return NewState(account, region), nil
	}
	if err != nil {
		oplog.Logger().Error("failed to read state file", "file", stateFile, "error", err)
		return nil, fmt.Errorf("reading state file: %w", err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		oplog.Logger().Error("failed to parse state file", "file", stateFile, "error", err)
		return nil, fmt.Errorf("parsing state file: %w", err)
	}
	return &st, nil
}

// WriteState persists state to the local cache file (.fabrica/state.json).
func WriteState(st *State) error {
	// #nosec G301 -- directory needs execute for traversal
	if err := os.MkdirAll(".fabrica", 0700); err != nil {
		oplog.Logger().Error("failed to create .fabrica directory", "error", err)
		return fmt.Errorf("creating .fabrica directory: %w", err)
	}
	// MarshalIndent cannot fail for State — it has no unexported fields,
	// no circular references, and no custom Marshaler.
	data, _ := json.MarshalIndent(st, "", "  ")
	if err := os.WriteFile(stateFile, data, 0600); err != nil {
		oplog.Logger().Error("failed to write state file", "file", stateFile, "error", err)
		return fmt.Errorf("writing state file: %w", err)
	}
	oplog.Logger().Debug("state written", "file", stateFile)
	return nil
}
