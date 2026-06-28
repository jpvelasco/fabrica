// Package provision holds the small, genuinely-shared helpers used by the
// module create commands (perforce, horde, workstation).
//
// Scope note: only substance-free boilerplate that was byte-identical across
// all three commands lives here — local-state reading, the confirmation phrase,
// and the confirmation instructions block. The provisioning flow itself
// (applyCreate: credentials, desired-state builders, plan-specific output) is
// deliberately NOT shared: those steps look alike but call module-specific code,
// so a generic engine would add indirection without removing real duplication.
// See issue #37 for the full rationale.
package provision

import (
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
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
