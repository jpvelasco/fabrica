package terminate

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/spf13/cobra"
)

var spec = teardown.Spec{
	ModuleName:     "workstation",
	Verb:           "terminate",
	VersionLabel:   "AMI ID",
	Title:          "Cloud Workstation",
	NotProvisioned: "Workstation is not provisioned. Nothing to terminate.",
	PlanHeader:     "Cloud Workstation — terminate plan",
	DryRunHeader:   "Cloud Workstation (terminate dry run)",
	Irreversible:   "IRREVERSIBLE: This will permanently delete the workstation and its data.",
	SuccessMessage: "Cloud workstation terminated.",
}

// NewTeardown builds this module's teardown.Command for orchestrated use by
// `fabrica destroy --all`. Confirmation is skipped (the orchestrator confirms
// the aggregate operation).
func NewTeardown(rt globals.Runtime, out io.Writer) teardown.Command {
	return teardown.NewTeardown(spec, rt, out)
}

// New returns the "workstation terminate" subcommand. Global flags (--dry-run,
// --yes, --json) are resolved at execution time via the source closures.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return teardown.NewStandaloneCommand(&cobra.Command{
		Use:   "terminate",
		Short: "Permanently terminate the cloud workstation",
		Long: `Permanently terminate the cloud workstation and all its AWS resources.

Resources are deleted in reverse-creation order to respect dependencies:
  1. EC2 Instance (terminated first)
  2. EC2 Security Group

State is updated after each deletion so a partial failure leaves a recoverable
record. Re-running terminate will skip resources that are already gone.

Before deleting the instance, the current EC2 state is checked:
  - stopping / shutting-down: terminate exits with an error; retry once complete.
  - terminated / not found: treated as already deleted; state is cleaned up.

With --dry-run, shows the terminate plan without making any AWS calls.`,
	}, spec, runtimeSource, optionsSource, out)
}
