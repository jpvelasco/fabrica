package perforce

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/perforce/create"
	"github.com/jpvelasco/fabrica/cmd/perforce/status"
	"github.com/spf13/cobra"
)

func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "perforce",
		Short: "Manage Perforce Helix Core",
		Long:  `Provision and manage a Perforce Helix Core server on AWS.`,
	}
	cmd.AddCommand(create.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(status.New(runtimeSource, optionsSource, out))
	return cmd
}
