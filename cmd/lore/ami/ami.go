package ami

import (
	"io"

	"github.com/spf13/cobra"
)

// New returns the "lore ami" parent command.
func New(out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ami",
		Short: "Tools for building a Lore AMI",
		Long: `Tools for building a Lore AMI.

Available operations:
  build   Generate files needed to build a Lore AMI`,
	}
	cmd.AddCommand(newBuildCmd(out))
	return cmd
}
