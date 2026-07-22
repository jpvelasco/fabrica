package start

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/workstation/action"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

// StartOutput is the JSON-serialisable result of a start run.
type StartOutput = action.ActionOutput

// New returns the "workstation start" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start a stopped cloud workstation EC2 instance",
		Long: `Start a previously stopped cloud workstation EC2 instance.

The workstation resumes from its saved state. DCV session setup may take
a minute or two after the instance comes online.

With --dry-run, shows what would happen without calling the EC2 API.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			ac := action.New(
				action.StartSpec,
				rt,
				opts.DryRun,
				opts.AssumeYes,
				opts.JSONOutput,
				out,
				prompt.ConfirmExact,
				action.DefaultExecuteAction(rt, action.StartVerb),
			)
			ac.SetReadState(func() (*fabricastate.State, error) {
				return action.DefaultReadStateForRuntime(rt)
			})
			ac.SetWriteState(action.DefaultWriteState)
			return ac.Run(cmd.Context())
		},
	}
}
