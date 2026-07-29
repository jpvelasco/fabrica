package destroy

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/spf13/cobra"
)

var spec = teardown.Spec{
	ModuleName:     "lore",
	Verb:           "destroy",
	VersionLabel:   "AMI ID",
	Title:          "Lore loreserver",
	NotProvisioned: "Lore is not provisioned. Nothing to destroy.",
	PlanHeader:     "Lore loreserver — destroy plan",
	DryRunHeader:   "Lore loreserver (destroy dry run)",
	Irreversible:   "IRREVERSIBLE: This will permanently delete the Lore server and its data.",
	SuccessMessage: "Lore loreserver destroyed.",
}

// NewTeardown builds this module's teardown.Command for orchestrated use by
// `fabrica destroy --all`. Confirmation is skipped (the orchestrator confirms
// the aggregate operation).
func NewTeardown(rt globals.Runtime, out io.Writer) teardown.Command {
	return teardown.NewTeardown(spec, rt, out)
}

// New returns the "lore destroy" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return teardown.NewStandaloneCommand(&cobra.Command{
		Use:   "destroy",
		Short: "Permanently delete the Lore server",
		Long: `Permanently delete the Lore loreserver and all its AWS resources.

Resources are deleted in reverse-creation order to respect dependencies:
  1. EC2 Instance (terminated first)
  2. EC2 Security Group

State is updated after each deletion so a partial failure leaves a recoverable
record. Re-running destroy will skip resources that are already gone.

With --dry-run, shows the destroy plan without making any AWS calls.`,
	}, spec, runtimeSource, optionsSource, out)
}
