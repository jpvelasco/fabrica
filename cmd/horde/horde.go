package horde

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/horde/ami"
	"github.com/jpvelasco/fabrica/cmd/horde/create"
	"github.com/jpvelasco/fabrica/cmd/horde/status"
	"github.com/jpvelasco/fabrica/cmd/horde/submit"
	"github.com/spf13/cobra"
)

// New returns the "horde" parent command with subcommands that cover the Horde
// coordinator lifecycle on AWS.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "horde",
		Short: "Manage Unreal Horde build coordinator",
		Long: `Manage an Unreal Horde build coordinator on AWS.

Available operations:
  create   Provision a new Horde coordinator on EC2
  status   Show coordinator health and connection info
  submit   Submit a BuildGraph job to the coordinator
  ami      Tools for building a Horde AMI`,
	}
	cmd.AddCommand(create.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(status.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(submit.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(ami.New(out))
	return cmd
}
