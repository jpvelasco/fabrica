package ddc

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/ddc/destroy"
	"github.com/jpvelasco/fabrica/cmd/ddc/region"
	"github.com/jpvelasco/fabrica/cmd/ddc/setup"
	"github.com/jpvelasco/fabrica/cmd/ddc/status"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/spf13/cobra"
)

// New returns the "ddc" parent command (setup, status, destroy, region add).
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ddc",
		Short: "Manage Distributed Derived Data Cache (Unreal Cloud DDC)",
		Long: `Manage a studio-wide Unreal Cloud DDC (Jupiter / Zen Cloud DDC) on AWS.

The home region is a single EC2 host (co-located coordinator + edge roles)
with hybrid EBS + S3 storage and an optional 1-node Scylla bootstrap.
Additional edge regions are added with 'ddc region add'.

Available operations:
  setup        Provision DDC infrastructure (home region)
  status       Show health and endpoints (home + edge regions)
  region add   Provision an additional DDC edge region
  destroy      Tear down DDC resources (all regions)`,
	}
	cmd.AddCommand(setup.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(status.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(region.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return cmd
}
