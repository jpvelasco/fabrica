// Package provision owns the shared orchestration used by module create
// commands. It centralizes lifecycle invariants and individual resource steps;
// module-specific plan construction, rendering, credentials, and desired-state
// builders remain with their commands. See issue #37 for that boundary.
package provision

import (
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
)

// ReadState reads the local state cache, seeding a fresh state with the
// configured account/region when the file does not exist yet.
func ReadState(rt globals.Runtime) (*fabricastate.State, error) {
	account, region := "", ""
	if rt.Config != nil {
		account = rt.Config.Cloud.AWS.AccountID
		region = rt.Config.Cloud.AWS.Region
	}
	return fabricastate.ReadStateOrNew(account, region)
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
