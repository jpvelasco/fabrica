// Package deploy wires the "deploy" parent command and its subcommands (setup,
// promote, rollback, status, destroy): GameLift deployment orchestration over
// CI/Horde-produced builds.
package deploy

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/deploy/destroy"
	"github.com/jpvelasco/fabrica/cmd/deploy/promote"
	"github.com/jpvelasco/fabrica/cmd/deploy/rollback"
	"github.com/jpvelasco/fabrica/cmd/deploy/setup"
	"github.com/jpvelasco/fabrica/cmd/deploy/status"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/spf13/cobra"
)

// New returns the "deploy" parent command.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Deploy game-server builds to GameLift fleets",
		Long: `Orchestrate GameLift deployment of CI/Horde-produced server builds.

Available operations:
  setup     Provision deploy infrastructure (IAM role + GameLift alias)
  promote   Register a build from S3 and roll it out to a new fleet (blue/green)
  rollback  Flip the alias back to the previous fleet
  status    Show fleet health, alias target, and rollback candidates
  destroy   Tear down fleets/builds (use --all to also remove alias + role)`,
	}
	cmd.AddCommand(setup.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(promote.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(rollback.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(status.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return cmd
}
