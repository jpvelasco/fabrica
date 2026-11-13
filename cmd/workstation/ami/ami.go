package ami

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/spf13/cobra"
)

// New returns the "workstation ami" parent command.
func New(runtimeSource globals.RuntimeSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ami",
		Short: "Tools for building a NICE DCV workstation AMI",
		Long: `Tools for building a NICE DCV workstation AMI.

Available operations:
  build   Generate files needed to build a DCV-ready workstation AMI`,
	}
	cmd.AddCommand(newBuildCmd(runtimeSource, out))
	return cmd
}
