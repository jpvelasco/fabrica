package ami

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/spf13/cobra"
)

// New returns the "horde ami" parent command.
func New(runtimeSource globals.RuntimeSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ami",
		Short: "Tools for building a Horde AMI",
		Long: `Tools for building a Horde AMI.

Available operations:
  build   Generate files needed to build a Horde AMI`,
	}
	cmd.AddCommand(newBuildCmd(runtimeSource, out))
	return cmd
}
