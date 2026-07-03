// Package ci wires the "ci" parent command and its subcommands (setup, trigger,
// status, logs): a CodeBuild-based orchestration layer over Horde.
package ci

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/ci/destroy"
	"github.com/jpvelasco/fabrica/cmd/ci/logs"
	"github.com/jpvelasco/fabrica/cmd/ci/setup"
	"github.com/jpvelasco/fabrica/cmd/ci/status"
	"github.com/jpvelasco/fabrica/cmd/ci/trigger"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/spf13/cobra"
)

// New returns the "ci" parent command.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ci",
		Short: "Manage CI pipelines that orchestrate Horde builds",
		Long: `Manage the Fabrica CI layer: a CodeBuild project that orchestrates Horde
BuildGraph jobs.

Available operations:
  setup    Provision the CI infrastructure (CodeBuild project + IAM role)
  trigger  Trigger a build run (submits a BuildGraph job to Horde)
  status   Show CI infrastructure and recent build status
  logs     Fetch logs for a specific build`,
	}
	cmd.AddCommand(setup.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(trigger.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(status.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(logs.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return cmd
}
