package stop

import (
	"context"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/workstation/action"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

// StopOutput is the JSON-serialisable result of a stop run.
type StopOutput = action.ActionOutput

type command struct {
	runtime   globals.Runtime
	dryRun    bool
	assumeYes bool
	jsonOut   bool
	out       io.Writer
	confirm   func(string, string) bool

	// seams for testing
	readState    func() (*fabricastate.State, error)
	writeState   func(*fabricastate.State) error
	stopInstance func(ctx context.Context, instanceID string) error
}

// New returns the "workstation stop" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
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

			c := command{
				runtime:   rt,
				dryRun:    opts.DryRun,
				assumeYes: opts.AssumeYes,
				jsonOut:   opts.JSONOutput,
				out:       out,
				confirm:   prompt.ConfirmExact,
			}
			c.readState = c.defaultReadState
			c.writeState = c.defaultWriteState
			if rt.Provider != nil {
				if mgr, ok := rt.Provider.(fabricac.EC2InstanceManager); ok {
					c.stopInstance = mgr.StopInstance
				}
			}
			return c.run(cmd.Context())
		},
	}
	return cmd
}

func (c command) run(ctx context.Context) error {
	ac := action.New(
		action.StopSpec,
		c.runtime,
		c.dryRun,
		c.assumeYes,
		c.jsonOut,
		c.out,
		c.confirm,
		func(ctx context.Context, instanceID string) error {
			if c.stopInstance == nil {
				return fmt.Errorf("no provider configured; run 'fabrica setup' first")
			}
			return c.stopInstance(ctx, instanceID)
		},
	)
	ac.SetReadState(c.readState)
	ac.SetWriteState(c.writeState)
	return ac.Run(ctx)
}

func (c command) defaultReadState() (*fabricastate.State, error) {
	account, region := "", ""
	if c.runtime.Config != nil {
		account = c.runtime.Config.Cloud.AWS.AccountID
		region = c.runtime.Config.Cloud.AWS.Region
	}
	return fabricastate.ReadStateOrNew(account, region)
}

func (c command) defaultWriteState(st *fabricastate.State) error {
	return fabricastate.WriteState(st)
}
