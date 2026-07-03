package destroy

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

var spec = teardown.Spec{
	ModuleName:     "perforce",
	Verb:           "destroy",
	VersionLabel:   "Version",
	Title:          "Perforce Helix Core",
	NotProvisioned: "Perforce is not provisioned. Nothing to destroy.",
	PlanHeader:     "Perforce Helix Core — destroy plan",
	DryRunHeader:   "Perforce Helix Core (destroy dry run)",
	Irreversible:   "IRREVERSIBLE: This will permanently delete the Perforce server and its data.",
	SuccessMessage: "Perforce Helix Core destroyed.",
}

// NewTeardown builds this module's teardown.Command for orchestrated use by
// `fabrica destroy --all`. Confirmation is skipped (the orchestrator confirms
// the aggregate operation).
func NewTeardown(rt globals.Runtime, out io.Writer) teardown.Command {
	tc := teardown.Command{
		Spec:        spec,
		Runtime:     rt,
		SkipConfirm: true,
		AssumeYes:   true,
		Out:         out,
		Confirm:     prompt.ConfirmExact,
		ReadState:   func() (*fabricastate.State, error) { return provision.ReadState(rt) },
		WriteState:  fabricastate.WriteState,
	}
	if rt.Provider != nil {
		if rc := rt.Provider.Resources(); rc != nil {
			tc.DeleteResource = rc.Delete
			tc.GetResource = rc.Get
		}
	}
	return tc
}

// New returns the "perforce destroy" subcommand. Global flags (--dry-run,
// --yes, --json) are resolved at execution time via the source closures.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "destroy",
		Short: "Permanently delete the Perforce Helix Core server",
		Long: `Permanently delete the Perforce Helix Core server and all its AWS resources.

Resources are deleted in reverse-creation order to respect dependencies:
  1. EC2 Instance (terminated first)
  2. EC2 Security Group

State is updated after each deletion so a partial failure leaves a recoverable
record. Re-running destroy will skip resources that are already gone.

Before deleting the instance, the current EC2 state is checked:
  - stopping / shutting-down: destroy exits with an error; retry once complete.
  - terminated / not found: treated as already deleted; state is cleaned up.

With --dry-run, shows the destroy plan without making any AWS calls.`,
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
