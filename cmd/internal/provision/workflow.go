package provision

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// CreateSpec describes the common lifecycle around a module-specific create
// plan. Plan construction, rendering, and resource creation remain owned by the
// calling command.
type CreateSpec[P any] struct {
	ModuleName      string
	Account         string
	Plan            P
	DryRun          bool
	AssumeYes       bool
	Out             io.Writer
	ExistingMessage string
	// ReadyStatuses are the recorded module statuses that count as fully
	// provisioned. A module recorded in any other status is accepted only
	// when its resource inventory covers RequiredResources.
	ReadyStatuses     []string
	RequiredResources []string
	// CleanupCommand is the module's removal command, shown in the
	// partial-module error so the operator knows how to recover.
	CleanupCommand string
	Confirm        func(string, string) bool
	ReadState      func() (*fabricastate.State, error)
	PrintDryRun    func(P)
	PrintApplyPlan func(P)
	Apply          func(context.Context, *fabricastate.State, P) error
	Runtime        globals.Runtime
	Operation      string
}

// RunCreate enforces the shared create lifecycle: dry runs stop before state
// access, existing modules stop before confirmation, and resource creation only
// begins after confirmation succeeds. Non-dry-run paths take the account-level
// state lock so concurrent creates cannot interleave the local/remote RMW.
func RunCreate[P any](ctx context.Context, spec CreateSpec[P]) error {
	if spec.DryRun {
		spec.PrintDryRun(spec.Plan)
		return nil
	}

	operation := spec.Operation
	if operation == "" {
		operation = spec.ModuleName + " create"
	}
	ctx, releaseLock, err := AcquireStateLock(ctx, spec.Runtime, operation)
	if err != nil {
		return err
	}
	defer releaseLock()

	st, err := spec.ReadState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	if mod := st.GetModule(spec.ModuleName); mod != nil {
		if missing := incompleteResources(spec.ReadyStatuses, spec.RequiredResources, mod); len(missing) > 0 {
			return partialModuleError(mod, missing, spec.CleanupCommand)
		}
		fmt.Fprint(spec.Out, spec.ExistingMessage)
		return nil
	}

	spec.PrintApplyPlan(spec.Plan)
	if !ConfirmCreate(spec.Out, spec.ModuleName, spec.Account, spec.AssumeYes, spec.Confirm) {
		return nil
	}

	return spec.Apply(ctx, st, spec.Plan)
}

// incompleteResources reports the required resource types a recorded module
// still lacks. A module whose status is in readyStatuses counts as fully
// provisioned as-is; every other status is judged by its recorded inventory,
// because create flows keep the module in "provisioning" until the status
// command's probe flips it to ready.
func incompleteResources(readyStatuses, required []string, mod *fabricastate.ModuleState) []string {
	for _, s := range readyStatuses {
		if s == mod.Status {
			return nil
		}
	}
	return missingResourceTypes(required, recordedTypeNames(mod))
}

// recordedTypeNames returns the TypeName of each resource recorded in a
// module, in recorded order.
func recordedTypeNames(mod *fabricastate.ModuleState) []string {
	types := make([]string, len(mod.Resources))
	for i, r := range mod.Resources {
		types[i] = r.TypeName
	}
	return types
}

// missingResourceTypes reports the required types whose required count
// exceeds their recorded count. The result is sorted so error output is
// deterministic.
func missingResourceTypes(required, recorded []string) []string {
	need := make(map[string]int, len(required))
	for _, t := range required {
		need[t]++
	}
	have := make(map[string]int, len(recorded))
	for _, t := range recorded {
		have[t]++
	}
	var missing []string
	for t, n := range need {
		if have[t] < n {
			missing = append(missing, t)
		}
	}
	sort.Strings(missing)
	return missing
}

// partialModuleError builds the failure for a recorded module whose inventory
// is incomplete. It states what is recorded, what a complete module needs,
// and the command that clears the way for a clean re-create.
func partialModuleError(mod *fabricastate.ModuleState, missing []string, cleanupCommand string) error {
	recorded := make([]string, 0, len(mod.Resources))
	seen := make(map[string]bool, len(mod.Resources))
	for _, t := range recordedTypeNames(mod) {
		if !seen[t] {
			seen[t] = true
			recorded = append(recorded, t)
		}
	}
	sort.Strings(recorded)
	recordedText := "no recorded resources"
	if len(recorded) > 0 {
		recordedText = strings.Join(recorded, ", ")
	}
	return fmt.Errorf(
		"%s state is recorded with status %q and %s, but a complete %s module needs %s.\n"+
			"This is a partially provisioned module from an interrupted create; fabrica will not create on top of it.\n"+
			"Run '%s' to remove the recorded resources, then run create again.",
		mod.Name,
		mod.Status,
		recordedText,
		mod.Name,
		strings.Join(missing, ", "),
		cleanupCommand,
	)
}
