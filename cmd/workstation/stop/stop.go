package stop

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/workstation/action"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

// StopOutput is the JSON-serialisable result of a stop run.
type StopOutput = action.ActionOutput

// New returns the "workstation stop" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the cloud workstation EC2 instance",
		Long: `Stop the cloud workstation EC2 instance to pause billing.

The workstation's data and configuration are preserved. Use
'fabrica workstation start' to bring it back online.

With --dry-run, shows what would happen without calling the EC2 API.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			ac := action.New(
				action.StopSpec,
				rt,
				opts.DryRun,
				opts.AssumeYes,
				opts.JSONOutput,
				out,
				prompt.ConfirmExact,
				action.DefaultExecuteAction(rt, action.StopVerb),
			)
			ac.SetReadState(func() (*fabricastate.State, error) {
				return action.DefaultReadStateForRuntime(rt)
			})
			ac.SetWriteState(action.DefaultWriteState)
			return ac.Run(cmd.Context())
		},
	}
}
