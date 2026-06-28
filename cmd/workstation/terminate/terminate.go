package terminate

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
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

// New returns the "workstation terminate" subcommand. Global flags (--dry-run,
// --yes, --json) are resolved at execution time via the source closures.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
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
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()

			c := teardown.Command{
				Spec:       spec,
				Runtime:    rt,
				DryRun:     opts.DryRun,
				AssumeYes:  opts.AssumeYes,
				JSONOut:    opts.JSONOutput,
				Out:        out,
				Confirm:    prompt.ConfirmExact,
				ReadState:  func() (*fabricastate.State, error) { return readState(rt) },
				WriteState: fabricastate.WriteState,
			}
			if rt.Provider != nil {
				c.DeleteResource = rt.Provider.Resources().Delete
				c.GetResource = rt.Provider.Resources().Get
			}
			return c.Run(cmd.Context())
		},
	}
}

func readState(rt globals.Runtime) (*fabricastate.State, error) {
	account, region := "", ""
	if rt.Config != nil {
		account = rt.Config.Cloud.AWS.AccountID
		region = rt.Config.Cloud.AWS.Region
	}
	return fabricastate.ReadStateOrNew(account, region)
}
