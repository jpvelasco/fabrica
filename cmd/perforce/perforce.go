package perforce

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/perforce/backup"
	"github.com/jpvelasco/fabrica/cmd/perforce/create"
	"github.com/jpvelasco/fabrica/cmd/perforce/destroy"
	"github.com/jpvelasco/fabrica/cmd/perforce/restore"
	"github.com/jpvelasco/fabrica/cmd/perforce/status"
	"github.com/spf13/cobra"
)

// New returns the "perforce" parent command with create, status, destroy,
// backup, and restore subcommands for the Helix Core lifecycle on AWS.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "perforce",
		Short: "Manage Perforce Helix Core",
		Long: `Manage a Perforce Helix Core version control server on AWS.

Available operations:
  create   Provision a new Helix Core server on EC2
  status   Show server health and P4PORT connection info
  backup   Create or manage EBS (optional S3) backups
  restore  Restore from a backup on the instance volume
  destroy  Permanently remove the server and its resources`,
	}
	cmd.AddCommand(create.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(status.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(backup.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(restore.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return cmd
}
