// Package agents provides the "horde agents" subcommand group for managing
// Horde build agent pools (Auto Scaling Group + Launch Template).
package agents

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/horde/agents/create"
	"github.com/jpvelasco/fabrica/cmd/horde/agents/destroy"
	"github.com/jpvelasco/fabrica/cmd/horde/agents/status"
	"github.com/spf13/cobra"
)

// New returns the "horde agents" parent command with subcommands for managing
// the Horde agent pool lifecycle.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Manage Horde build agent pool",
		Long: `Manage a pool of Horde build agents on AWS.

Agents run in an Auto Scaling Group launched from a Launch Template,
enroll against the existing Horde coordinator, and are fully managed
by Fabrica state.

Available operations:
  create   Provision an agent pool (ASG + Launch Template)
  status   Show agent pool capacity and coordinator endpoint
  destroy  Permanently delete the agent pool and its AWS resources`,
	}
	cmd.AddCommand(create.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(status.New(runtimeSource, optionsSource, out))
	cmd.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return cmd
}
