package stop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const (
	lineWidth  = 58
	moduleName = "workstation"
)

// StopOutput is the JSON-serialisable result of a stop run.
type StopOutput struct {
	InstanceID string `json:"instanceId"`
	Status     string `json:"status"`
	DryRun     bool   `json:"dryRun"`
}

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
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	m := st.GetModule(moduleName)
	if m == nil {
		if c.jsonOut {
			c.printJSON(StopOutput{Status: "not_provisioned", DryRun: c.dryRun})
			return nil
		}
		fmt.Fprintln(c.out, "Workstation is not provisioned. Nothing to stop.")
		return nil
	}

	instRes, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance")
	if !ok || instRes.Identifier == "" {
		return fmt.Errorf("workstation has no instance in state; run 'fabrica workstation list' to inspect")
	}
	instanceID := instRes.Identifier

	if m.Status == "stopped" {
		if c.jsonOut {
			c.printJSON(StopOutput{InstanceID: instanceID, Status: "already_stopped", DryRun: c.dryRun})
			return nil
		}
		fmt.Fprintf(c.out, "Instance %s is already stopped.\n", instanceID)
		return nil
	}

	if c.dryRun {
		c.printDryRun(m, instanceID)
		return nil
	}

	if !c.jsonOut {
		c.printStopPlan(m, instanceID)
	}

	if !c.assumeYes {
		fmt.Fprintln(c.out)
		phrase := confirmPhrase(instanceID)
		c.printConfirmInstructions(phrase)
		if !c.confirm("Enter confirmation phrase", phrase) {
			fmt.Fprintln(c.out, "Cancelled. No AWS calls were made.")
			return nil
		}
		fmt.Fprintln(c.out, "Confirmation accepted.")
	} else if !c.jsonOut {
		fmt.Fprintln(c.out, "Proceeding without interactive confirmation (--yes flag set).")
	}

	return c.applyStop(ctx, st, m, instanceID)
}

func (c command) applyStop(ctx context.Context, st *fabricastate.State, m *fabricastate.ModuleState, instanceID string) error {
	if c.stopInstance == nil {
		return fmt.Errorf("no provider configured; run 'fabrica setup' first")
	}

	if !c.jsonOut {
		fmt.Fprintf(c.out, "Stopping instance %s...\n", instanceID)
	}

	if err := c.stopInstance(ctx, instanceID); err != nil {
		return fmt.Errorf("stopping instance %s: %w", instanceID, err)
	}

	st.UpsertModule(moduleName, m.Version, "stopped", m.Resources)
	if err := c.writeState(st); err != nil {
		fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
	}

	if c.jsonOut {
		c.printJSON(StopOutput{InstanceID: instanceID, Status: "stopped", DryRun: false})
		return nil
	}

	fmt.Fprintf(c.out, "  Instance %s stopped.\n", instanceID)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Run 'fabrica workstation start' to bring it back online.")
	return nil
}

func (c command) printDryRun(m *fabricastate.ModuleState, instanceID string) {
	if c.jsonOut {
		c.printJSON(StopOutput{InstanceID: instanceID, Status: "would_stop", DryRun: true})
		return
	}
	fmt.Fprintln(c.out, "Cloud Workstation (stop dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Instance ID: %s\n", instanceID)
	fmt.Fprintf(c.out, "  Status:      %s\n", m.Status)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Would stop the EC2 instance.")
	fmt.Fprintln(c.out, "Run without --dry-run to proceed.")
}

func (c command) printStopPlan(m *fabricastate.ModuleState, instanceID string) {
	fmt.Fprintln(c.out, "Cloud Workstation — stop")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Instance ID: %s\n", instanceID)
	fmt.Fprintf(c.out, "  Status:      %s\n", m.Status)
	fmt.Fprintln(c.out)
}

func (c command) printJSON(out StopOutput) {
	data, _ := json.MarshalIndent(out, "", "  ")
	fmt.Fprintln(c.out, string(data))
}

func confirmPhrase(instanceID string) string {
	return fmt.Sprintf("stop workstation %s", instanceID)
}

func (c command) printConfirmInstructions(phrase string) {
	fmt.Fprintln(c.out, "Confirmation required.")
	fmt.Fprintln(c.out, "Type this exact phrase to continue:")
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  %s\n", phrase)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Any other input cancels.")
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
