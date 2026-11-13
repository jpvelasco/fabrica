// Package provision owns the shared orchestration used by module create
// commands. It centralizes lifecycle invariants and individual resource steps;
// module-specific plan construction, rendering, credentials, and desired-state
// builders remain with their commands. See issue #37 for that boundary.
package provision

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/oplog"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/jpvelasco/fabrica/internal/tags"
)

// ReadState loads the canonical provisioning state for the runtime. When the
// provider implements cloud.StateStore and the account is bootstrapped, the
// remote snapshot wins: a missing document falls back to the local cache, and
// a present one is validated against the configured account/region/ownership and
// refreshed into the local cache before use. Storage failures surface to the
// caller. Mutating flows call this while holding the account lock; read-only
// flows may call it without the lock (S3 reads are atomic).
func ReadState(ctx context.Context, rt globals.Runtime) (*fabricastate.State, error) {
	identity, err := rt.Config.ResolveIdentity()
	if err != nil {
		return nil, err
	}
	account, region := identitySides(rt)
	path := StatePath(rt)
	store, bucket := stateStore(rt)
	if store == nil {
		return readLocalState(path, account, region, identity)
	}

	doc, err := store.GetStateDocument(ctx, bucket, fabricastate.StateDocumentKey)
	if err != nil {
		if !errors.Is(err, fabricac.ErrStateDocumentMissing) {
			return nil, fmt.Errorf("reading remote state: %w", err)
		}
		st, err := readLocalState(path, account, region, identity)
		if err != nil {
			return nil, err
		}
		if len(st.Modules) == 0 {
			oplog.Logger().Debug("remote state empty; no modules provisioned yet", "bucket", bucket)
		} else {
			oplog.Logger().Warn("remote state document not found; using local state cache", "bucket", bucket)
		}
		return st, nil
	}

	st, err := fabricastate.ParseState(doc, account, region)
	if err != nil {
		return nil, fmt.Errorf("reading remote state: %w", err)
	}
	if err := st.ValidateOwnership(identity); err != nil {
		return nil, fmt.Errorf("reading remote state: %w", err)
	}
	if err := fabricastate.WriteStateTo(st, path); err != nil {
		oplog.Logger().Warn("remote state loaded; local cache refresh failed", "file", path, "error", err)
	}
	return st, nil
}

func readLocalState(path, account, region string, identity tags.Identity) (*fabricastate.State, error) {
	st, err := fabricastate.ReadStateFile(path, account, region)
	if err != nil {
		return nil, err
	}
	if err := st.ValidateOwnership(identity); err != nil {
		return nil, err
	}
	return st, nil
}

// StatePath returns the local state file for the runtime's config file, so
// each profile keeps its own cache under .fabrica/.
func StatePath(rt globals.Runtime) string {
	return fabricastate.StateFileFor(rt.ConfigFile())
}

// StateWriter returns a write seam bound to the runtime's state backend.
// Ownership is validated before storage changes; fresh compatible state is
// bound to the configured owner. Every successful mutation is persisted remotely
// first (when the backend is available), then the local cache is refreshed.
// A remote write failure fails the mutation and leaves the cache untouched so
// a retry re-reads the last persisted state. A local cache failure after a
// successful remote write is a warning — the remote document is the record
// of truth and the next read refreshes the cache.
func StateWriter(ctx context.Context, rt globals.Runtime) func(*fabricastate.State) error {
	persistence := NewStatePersistence(ctx, rt)
	return func(st *fabricastate.State) error {
		_, err := persistence.Persist(st, StateWriteAllowLocal)
		return err
	}
}

// identitySides extracts the configured account and home region used to
// validate state documents. Empty values accept anything (an unknown side
// cannot prove a mismatch).
func identitySides(rt globals.Runtime) (string, string) {
	if rt.Config == nil {
		return "", ""
	}
	return rt.Config.Cloud.AWS.AccountID, rt.Config.Cloud.AWS.Region
}

// stateStore resolves the remote state backend for the runtime: the provider
// capability plus the bucket from state.ResolveBackendNames. It returns
// (nil, "") when the provider lacks the capability or when the account is not
// bootstrapped (no account ID and no explicit bucket), where no remote
// document can exist.
func stateStore(rt globals.Runtime) (fabricac.StateStore, string) {
	if rt.Config == nil {
		return nil, ""
	}
	store, ok := rt.Provider.(fabricac.StateStore)
	if !ok {
		return nil, ""
	}
	if rt.Config.Cloud.AWS.AccountID == "" && rt.Config.State.Bucket == "" {
		return nil, ""
	}
	return store, fabricastate.ResolveBackendNames(rt.Config, rt.Config.Cloud.AWS.AccountID).Bucket
}

// ConfirmPhrase is the exact phrase a user must type to confirm a create:
// "create <module> <account>".
func ConfirmPhrase(module, account string) string {
	return fmt.Sprintf("create %s %s", module, account)
}

// PrintConfirmInstructions prints the standard typed-confirmation prompt.
func PrintConfirmInstructions(out io.Writer, phrase string) {
	fmt.Fprintln(out, "Confirmation required.")
	fmt.Fprintln(out, "Type this exact phrase to continue:")
	fmt.Fprintln(out)
	fmt.Fprintf(out, "  %s\n", phrase)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Any other input cancels.")
}

// ConfirmCreate handles the interactive confirmation flow for create commands.
// If assumeYes is true, skips confirmation and prints a bypass message.
// Returns true if the operation should proceed, false if the user cancelled.
func ConfirmCreate(out io.Writer, moduleName, account string, assumeYes bool, confirm func(prompt, response string) bool) bool {
	if assumeYes {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Proceeding without interactive confirmation (--yes flag set).")
		return true
	}
	fmt.Fprintln(out)
	phrase := ConfirmPhrase(moduleName, account)
	PrintConfirmInstructions(out, phrase)
	if !confirm("Enter confirmation phrase", phrase) {
		fmt.Fprintln(out, "Cancelled. No AWS calls were made.")
		return false
	}
	fmt.Fprintln(out, "Confirmation accepted.")
	return true
}

// CreateResourcesPrompt is the standard confirmation prompt for module setup.
const CreateResourcesPrompt = "Create these resources?"

// ConfirmSetup handles the simple yes/no confirmation shared by setup
// commands. If assumeYes is true, it skips the prompt and reports the bypass.
// Returns true if setup should proceed, false if the user cancelled.
func ConfirmSetup(out io.Writer, prompt string, assumeYes bool, confirm func(string) bool) bool {
	if assumeYes {
		fmt.Fprintln(out, "Proceeding without confirmation (--yes set).")
		return true
	}
	if confirm(prompt) {
		return true
	}
	fmt.Fprintln(out, "Setup cancelled. No AWS resources were created.")
	return false
}

// ExistingResource returns the module resource of the given type from current
// state, if present — used to skip already-provisioned resources idempotently.
// Returns (zero value, false) when the module or resource is not found.
func ExistingResource(st *fabricastate.State, moduleName, typeName string) (fabricastate.ModuleResource, bool) {
	m := st.GetModule(moduleName)
	if m == nil {
		return fabricastate.ModuleResource{}, false
	}
	return stateutil.ResourceByType(m, typeName)
}

// AppendUnique appends r to resources only if no resource with the same
// TypeName already exists. Returns the (possibly extended) slice.
func AppendUnique(resources []fabricastate.ModuleResource, r fabricastate.ModuleResource) []fabricastate.ModuleResource {
	for _, existing := range resources {
		if existing.TypeName == r.TypeName {
			return resources
		}
	}
	return append(resources, r)
}
