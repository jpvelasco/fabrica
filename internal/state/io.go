package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jpvelasco/fabrica/internal/oplog"
	"github.com/jpvelasco/fabrica/internal/tags"
)

// DefaultStateFile is the local state cache for the default config
// (fabrica.yaml). Every other config file gets its own namespaced file so
// profiles and accounts never share one cache.
const DefaultStateFile = ".fabrica/state.json"

// StateDocumentKey is the object key of the canonical state document in the
// account's state bucket. The remote document is the record of truth; the
// local cache file mirrors it.
const StateDocumentKey = "state.json"

// StateFileFor returns the local state cache path for the given config file.
// The default config (fabrica.yaml, or an empty path) maps to
// DefaultStateFile; every other config file maps to .fabrica/state-<name>.json
// where <name> is the base file name without its .yaml/.yml extension and any
// "fabrica-" prefix. Two config files that resolve to the same name share a
// file on purpose — ValidateIdentity rejects the read if their targets differ.
func StateFileFor(configFile string) string {
	if configFile == "" {
		return DefaultStateFile
	}
	base := filepath.Base(configFile)
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".yaml"), ".yml")
	base = strings.TrimPrefix(base, "fabrica-")
	if base == "fabrica" || base == "." {
		return DefaultStateFile
	}
	return filepath.Join(".fabrica", "state-"+sanitizeStateName(base)+".json")
}

// sanitizeStateName maps characters that are invalid in file names (on any
// platform) to underscores so a sloppy or hostile config file name cannot
// break the state path.
func sanitizeStateName(name string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', ' ':
			return '_'
		}
		return r
	}, name)
}

// ValidateIdentity rejects loaded state whose recorded account or home region
// disagrees with the runtime target. Empty sides are accepted: an unknown side
// cannot prove a mismatch, and DDC edges keep the home region recorded even
// though they run in another region.
func ValidateIdentity(st *State, account, region string) error {
	if st == nil {
		return nil
	}
	var problems []string
	if st.Account != "" && account != "" && st.Account != account {
		problems = append(problems, fmt.Sprintf("recorded account %s vs config %s", st.Account, account))
	}
	if st.Region != "" && region != "" && st.Region != region {
		problems = append(problems, fmt.Sprintf("recorded home region %s vs config %s", st.Region, region))
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("state target mismatch (%s); run with the --profile or config file that recorded this state, or delete the local state file and the remote state document to rebind this working directory", strings.Join(problems, "; "))
}

// ParseState unmarshals a state document (remote object body or local cache
// content) and validates it against the expected account and region. Legacy
// documents bind to default ownership; explicit invalid bindings fail closed.
func ParseState(data []byte, account, region string) (*State, error) {
	var st State
	doc := struct {
		*State
		ManagedBy json.RawMessage `json:"managedBy"`
	}{State: &st}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing state document: %w", err)
	}
	value := tags.ManagedByValue
	if doc.ManagedBy != nil {
		value = ""
		if err := json.Unmarshal(doc.ManagedBy, &value); err != nil {
			return nil, fmt.Errorf("parsing state managedBy: %w", err)
		}
		if value == "" {
			return nil, fmt.Errorf("state managedBy must be a nonempty string; restore the document's recorded ownership")
		}
	}
	identity, err := tags.ResolveIdentity(value)
	if err != nil {
		return nil, fmt.Errorf("parsing state managedBy: %w", err)
	}
	st.ManagedBy = identity.ManagedBy()
	st.loadedOwner = st.ManagedBy
	if err := ValidateIdentity(&st, account, region); err != nil {
		return nil, err
	}
	return &st, nil
}

// ValidateOwnership rejects a different owner or a changed loaded binding.
// A fresh unbound state is accepted without assigning ownership during reads.
func (s *State) ValidateOwnership(identity tags.Identity) error {
	if s == nil {
		return fmt.Errorf("no state available; read state before persisting it")
	}
	if s.loadedOwner != "" && s.ManagedBy != s.loadedOwner {
		return fmt.Errorf("state ownership binding changed from %q to %q; restore the recorded binding and its original configuration", s.loadedOwner, s.ManagedBy)
	}
	if s.ManagedBy == "" {
		return nil
	}
	recorded, err := tags.ResolveIdentity(s.ManagedBy)
	if err != nil {
		return fmt.Errorf("invalid state managedBy: %w", err)
	}
	if recorded.ManagedBy() != identity.ManagedBy() {
		return fmt.Errorf("state ownership mismatch (recorded %q vs configured %q); restore the configuration that created this deployment", recorded.ManagedBy(), identity.ManagedBy())
	}
	return nil
}

// BindOwnership binds only a compatible state, never replacing loaded ownership.
func (s *State) BindOwnership(identity tags.Identity) error {
	if err := s.ValidateOwnership(identity); err != nil {
		return err
	}
	s.ManagedBy = identity.ManagedBy()
	s.loadedOwner = s.ManagedBy
	return nil
}

// EncodeState renders the canonical state document. The remote object body
// and the local cache share this encoding. A decoded legacy document serializes
// its resolved default binding when cached; reading never writes it remotely.
//
// MarshalIndent cannot fail for State — it has no circular references or custom
// Marshaler, and unexported binding bookkeeping is excluded.
func EncodeState(st *State) []byte {
	data, _ := json.MarshalIndent(st, "", "  ")
	return data
}

// ReadStateFile reads state from the local cache at path. If the file does not
// exist, it returns a fresh empty state initialised with the provided account
// and region — the caller should pass empty strings if those values are not
// yet known. An existing file whose recorded account or home region conflicts
// with the target is rejected before any caller can use it.
func ReadStateFile(path, account, region string) (*State, error) {
	// #nosec G304 -- local state cache path derived from the config file name (StateFileFor) and confined to .fabrica/
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		oplog.Logger().Debug("state file not found, creating new state", "file", path)
		return NewState(account, region), nil
	}
	if err != nil {
		oplog.Logger().Error("failed to read state file", "file", path, "error", err)
		return nil, fmt.Errorf("reading state file: %w", err)
	}
	st, err := ParseState(data, account, region)
	if err != nil {
		oplog.Logger().Error("failed to parse state file", "file", path, "error", err)
		return nil, fmt.Errorf("reading state file: %w", err)
	}
	return st, nil
}

// WriteStateTo persists state to the local cache file at path, creating the
// .fabrica directory when needed.
func WriteStateTo(st *State, path string) error {
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- directory requires execute bit for traversal
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		oplog.Logger().Error("failed to create .fabrica directory", "error", err)
		return fmt.Errorf("creating state directory: %w", err)
	}
	if err := os.WriteFile(path, EncodeState(st), 0600); err != nil {
		oplog.Logger().Error("failed to write state file", "file", path, "error", err)
		return fmt.Errorf("writing state file: %w", err)
	}
	oplog.Logger().Debug("state written", "file", path)
	return nil
}

// WriteState persists state to DefaultStateFile.
func WriteState(st *State) error {
	return WriteStateTo(st, DefaultStateFile)
}
