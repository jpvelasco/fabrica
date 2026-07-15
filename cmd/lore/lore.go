package lore

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/lore/create"
	"github.com/jpvelasco/fabrica/cmd/lore/destroy"
	"github.com/jpvelasco/fabrica/cmd/lore/status"
	"github.com/spf13/cobra"
)

// New returns the "lore" parent command with create, status, and destroy
// subcommands that together cover the full Lore loreserver lifecycle on AWS.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lore",
		Short: "Manage Lore version control server",
		Long: `Manage an Epic Lore (loreserver) version control server on AWS.

Available operations:
  create   Provision a new Lore server on EC2
  status   Show server health and connection info
  destroy  Permanently remove the server and its resources

Lore is offered in parallel with Perforce — studios may run either or both.`,
	}
	cmd.AddCommand(create.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(status.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return cmd
}
