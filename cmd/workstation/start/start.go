package start

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

// StartOutput is the JSON-serialisable result of a start run.
type StartOutput struct {
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
	confirm   func(string) bool

	// seams for testing
	readState     func() (*fabricastate.State, error)
	writeState    func(*fabricastate.State) error
	startInstance func(ctx context.Context, instanceID string) error
}

// New returns the "workstation start" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
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

			c := command{
				runtime:   rt,
				dryRun:    opts.DryRun,
				assumeYes: opts.AssumeYes,
				jsonOut:   opts.JSONOutput,
				out:       out,
				confirm:   prompt.Confirm,
			}
			c.readState = c.defaultReadState
			c.writeState = c.defaultWriteState
			if rt.Provider != nil {
				if mgr, ok := rt.Provider.(fabricac.EC2InstanceManager); ok {
					c.startInstance = mgr.StartInstance
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
			c.printJSON(StartOutput{Status: "not_provisioned", DryRun: c.dryRun})
			return nil
		}
		fmt.Fprintln(c.out, "Workstation is not provisioned. Nothing to start.")
		return nil
	}

	instRes, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance")
	if !ok || instRes.Identifier == "" {
		return fmt.Errorf("workstation has no instance in state; run 'fabrica workstation list' to inspect")
	}
	instanceID := instRes.Identifier

	if m.Status == "ready" || m.Status == "provisioning" {
		if c.jsonOut {
			c.printJSON(StartOutput{InstanceID: instanceID, Status: "already_running", DryRun: c.dryRun})
			return nil
		}
		fmt.Fprintf(c.out, "Instance %s is already running (status: %s).\n", instanceID, m.Status)
		return nil
	}

	if c.dryRun {
		c.printDryRun(m, instanceID)
		return nil
	}

	if !c.jsonOut {
		c.printStartPlan(m, instanceID)
	}

	if !c.assumeYes {
		fmt.Fprintln(c.out)
		if !c.confirm(fmt.Sprintf("Start workstation instance %s?", instanceID)) {
			fmt.Fprintln(c.out, "Cancelled. No AWS calls were made.")
			return nil
		}
	} else if !c.jsonOut {
		fmt.Fprintln(c.out, "Proceeding without interactive confirmation (--yes flag set).")
	}

	return c.applyStart(ctx, st, m, instanceID)
}

func (c command) applyStart(ctx context.Context, st *fabricastate.State, m *fabricastate.ModuleState, instanceID string) error {
	if c.startInstance == nil {
		return fmt.Errorf("no provider configured; run 'fabrica setup' first")
	}

	if !c.jsonOut {
		fmt.Fprintf(c.out, "Starting instance %s...\n", instanceID)
	}

	if err := c.startInstance(ctx, instanceID); err != nil {
		return fmt.Errorf("starting instance %s: %w", instanceID, err)
	}

	st.UpsertModule(moduleName, m.Version, "ready", m.Resources)
	if err := c.writeState(st); err != nil {
		fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
	}

	if c.jsonOut {
		c.printJSON(StartOutput{InstanceID: instanceID, Status: "ready", DryRun: false})
		return nil
	}

	fmt.Fprintf(c.out, "  Instance %s started.\n", instanceID)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Run 'fabrica workstation list' to view connection details.")
	return nil
}

func (c command) printDryRun(m *fabricastate.ModuleState, instanceID string) {
	if c.jsonOut {
		c.printJSON(StartOutput{InstanceID: instanceID, Status: "would_start", DryRun: true})
		return
	}
	fmt.Fprintln(c.out, "Cloud Workstation (start dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Instance ID: %s\n", instanceID)
	fmt.Fprintf(c.out, "  Status:      %s\n", m.Status)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Would start the EC2 instance.")
	fmt.Fprintln(c.out, "Run without --dry-run to proceed.")
}

func (c command) printStartPlan(m *fabricastate.ModuleState, instanceID string) {
	fmt.Fprintln(c.out, "Cloud Workstation — start")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Instance ID: %s\n", instanceID)
	fmt.Fprintf(c.out, "  Status:      %s\n", m.Status)
	fmt.Fprintln(c.out)
}

func (c command) printJSON(out StartOutput) {
	data, _ := json.MarshalIndent(out, "", "  ")
	fmt.Fprintln(c.out, string(data))
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
