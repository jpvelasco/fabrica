package workstation

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/workstation/create"
	"github.com/jpvelasco/fabrica/cmd/workstation/list"
	"github.com/jpvelasco/fabrica/cmd/workstation/start"
	"github.com/jpvelasco/fabrica/cmd/workstation/stop"
	"github.com/jpvelasco/fabrica/cmd/workstation/terminate"
	"github.com/spf13/cobra"
)

// New returns the "workstation" parent command with all subcommands.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "workstation",
		Short: "Manage cloud workstations",
		Long: `Manage NICE DCV cloud workstations on AWS.

Available operations:
  create     Provision a new cloud workstation on EC2
  list       Show provisioned workstations
  stop       Stop the workstation instance (pauses billing)
  start      Start a stopped workstation instance
  terminate  Permanently terminate the workstation and all its AWS resources`,
	}
	cmd.AddCommand(create.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(list.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(stop.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(start.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(terminate.New(runtimeSource, optionsSource, out))
	return cmd
}
