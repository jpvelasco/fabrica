package ami

import (
	"io"

	"github.com/spf13/cobra"
)

// New returns the "ddc ami" parent command.
func New(out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ami",
		Short: "Tools for building a DDC AMI",
		Long: `Tools for building an Unreal Cloud DDC AMI.

Available operations:
  build   Generate local Image Builder artifacts for a DDC AMI`,
	}
	cmd.AddCommand(newBuildCmd(out))
	return cmd
}
