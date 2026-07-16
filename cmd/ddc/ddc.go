package ddc

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/ddc/destroy"
	"github.com/jpvelasco/fabrica/cmd/ddc/setup"
	"github.com/jpvelasco/fabrica/cmd/ddc/status"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/spf13/cobra"
)

// New returns the "ddc" parent command (setup, status, destroy).
// V1 is single home-region only — no region add.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ddc",
		Short: "Manage Distributed Derived Data Cache (Unreal Cloud DDC)",
		Long: `Manage a studio-wide Unreal Cloud DDC (Jupiter / Zen Cloud DDC) on AWS.

V1 provisions a single home-region EC2 host (co-located coordinator + edge roles),
hybrid EBS + S3 storage, optional 1-node Scylla bootstrap, and server-only endpoints.

Available operations:
  setup    Provision DDC infrastructure
  status   Show health and endpoints
  destroy  Tear down DDC resources

There is no region add (or any multi-region command) in V1 — deferred to a later milestone.`,
	}
	cmd.AddCommand(setup.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(status.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return cmd
}
