// Package action owns the shared workstation start and stop command pattern.
package action

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const (
	lineWidth  = 58
	moduleName = "workstation"
	startVerb  = "start"
	stopVerb   = "stop"
)

// ActionOutput is the JSON-serializable result of a workstation start or stop.
type ActionOutput struct {
	InstanceID string `json:"instanceId"`
	Status     string `json:"status"`
	DryRun     bool   `json:"dryRun"`
}

type spec struct {
	verb              string
	short             string
	long              string
	progressText      string
	targetStatus      string
	alreadyActiveCode string
	dryRunStatus      string
	dryRunText        string
	successText       string
	followUpText      string
	isAlreadyActive   func(status string) bool
	alreadyActiveText func(instanceID, status string) string
}

type command struct {
	spec      spec
	dryRun    bool
	assumeYes bool
	jsonOut   bool
	out       io.Writer
	confirm   func(string, string) bool

	readState     func() (*fabricastate.State, error)
	writeState    func(*fabricastate.State) error
	executeAction func(context.Context, string) error
}

type commandOptions struct {
	dryRun        bool
	assumeYes     bool
	jsonOut       bool
	out           io.Writer
	confirm       func(string, string) bool
	readState     func() (*fabricastate.State, error)
	writeState    func(*fabricastate.State) error
	executeAction func(context.Context, string) error
}

func newCommand(commandSpec spec, opts commandOptions) *command {
	return &command{
		spec:          commandSpec,
		dryRun:        opts.dryRun,
		assumeYes:     opts.assumeYes,
		jsonOut:       opts.jsonOut,
		out:           opts.out,
		confirm:       opts.confirm,
		readState:     opts.readState,
		writeState:    opts.writeState,
		executeAction: opts.executeAction,
	}
}

// NewStart returns the "workstation start" subcommand.
func NewStart(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return newCobraCommand(startSpec(), runtimeSource, optionsSource, out)
}

// NewStop returns the "workstation stop" subcommand.
func NewStop(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return newCobraCommand(stopSpec(), runtimeSource, optionsSource, out)
}

func newCobraCommand(commandSpec spec, runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   commandSpec.verb,
		Short: commandSpec.short,
		Long:  commandSpec.long,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}

			opts := optionsSource()
			actionCommand := newCommand(commandSpec, commandOptions{
				dryRun:    opts.DryRun,
				assumeYes: opts.AssumeYes,
				jsonOut:   opts.JSONOutput,
				out:       out,
				confirm:   prompt.ConfirmExact,
				readState: func() (*fabricastate.State, error) {
					return provision.ReadState(rt)
				},
				writeState:    fabricastate.WriteState,
				executeAction: defaultExecuteAction(rt, commandSpec.verb),
			})
			return actionCommand.run(cmd.Context())
		},
	}
}

func (c *command) run(ctx context.Context) error {
	st, instanceID, err := c.validatePreAction()
	if err != nil {
		return err
	}
	if st == nil {
		return nil
	}

	m := st.GetModule(moduleName)
	if c.spec.isAlreadyActive(m.Status) {
		c.printAlreadyActive(instanceID, m.Status)
		return nil
	}

	if c.dryRun {
		c.printDryRun(m, instanceID)
		return nil
	}

	if !c.jsonOut {
		c.printPlan(m, instanceID)
	}
	if !c.confirmAction(instanceID) {
		return nil
	}

	return c.apply(ctx, st, m, instanceID)
}

func (c *command) validatePreAction() (*fabricastate.State, string, error) {
	st, err := c.readState()
	if err != nil {
		return nil, "", fmt.Errorf("reading state: %w", err)
	}

	m := st.GetModule(moduleName)
	if m == nil {
		c.printNotProvisioned()
		return nil, "", nil
	}

	instance, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance")
	if !ok || instance.Identifier == "" {
		return nil, "", fmt.Errorf("workstation has no instance in state; run 'fabrica workstation list' to inspect")
	}
	return st, instance.Identifier, nil
}

func (c *command) printNotProvisioned() {
	if c.jsonOut {
		modstatus.WriteJSON(c.out, ActionOutput{Status: "not_provisioned", DryRun: c.dryRun})
		return
	}
	fmt.Fprintln(c.out, "Workstation is not provisioned. Nothing to "+c.spec.verb+".")
}

func (c *command) printAlreadyActive(instanceID, status string) {
	if c.jsonOut {
		modstatus.WriteJSON(c.out, ActionOutput{InstanceID: instanceID, Status: c.spec.alreadyActiveCode, DryRun: c.dryRun})
		return
	}
	fmt.Fprintln(c.out, c.spec.alreadyActiveText(instanceID, status))
}

func (c *command) confirmAction(instanceID string) bool {
	if c.assumeYes {
		if !c.jsonOut {
			fmt.Fprintln(c.out, "Proceeding without interactive confirmation (--yes flag set).")
		}
		return true
	}

	fmt.Fprintln(c.out)
	phrase := c.confirmPhrase(instanceID)
	provision.PrintConfirmInstructions(c.out, phrase)
	if !c.confirm("Enter confirmation phrase", phrase) {
		fmt.Fprintln(c.out, "Cancelled. No AWS calls were made.")
		return false
	}
	fmt.Fprintln(c.out, "Confirmation accepted.")
	return true
}

func (c *command) apply(ctx context.Context, st *fabricastate.State, m *fabricastate.ModuleState, instanceID string) error {
	if c.executeAction == nil {
		return fmt.Errorf("no provider configured; run 'fabrica setup' first")
	}

	if !c.jsonOut {
		fmt.Fprintf(c.out, c.spec.progressText+" instance %s...\n", instanceID)
	}
	if err := c.executeAction(ctx, instanceID); err != nil {
		return fmt.Errorf("%s instance %s: %w", strings.ToLower(c.spec.progressText), instanceID, err)
	}

	st.UpsertModule(moduleName, m.Version, c.spec.targetStatus, m.Resources)
	if err := c.writeState(st); err != nil {
		fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
	}

	if c.jsonOut {
		modstatus.WriteJSON(c.out, ActionOutput{InstanceID: instanceID, Status: c.spec.targetStatus})
		return nil
	}

	fmt.Fprintf(c.out, "  Instance %s "+c.spec.successText+".\n", instanceID)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, c.spec.followUpText)
	return nil
}

func (c *command) printDryRun(m *fabricastate.ModuleState, instanceID string) {
	if c.jsonOut {
		modstatus.WriteJSON(c.out, ActionOutput{InstanceID: instanceID, Status: c.spec.dryRunStatus, DryRun: true})
		return
	}

	fmt.Fprintln(c.out, "Cloud Workstation ("+c.spec.verb+" dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Instance ID: %s\n", instanceID)
	fmt.Fprintf(c.out, "  Status:      %s\n", m.Status)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, c.spec.dryRunText)
	fmt.Fprintln(c.out, "Run without --dry-run to proceed.")
}

func (c *command) printPlan(m *fabricastate.ModuleState, instanceID string) {
	fmt.Fprintln(c.out, "Cloud Workstation — "+c.spec.verb)
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Instance ID: %s\n", instanceID)
	fmt.Fprintf(c.out, "  Status:      %s\n", m.Status)
	fmt.Fprintln(c.out)
}

func (c *command) confirmPhrase(instanceID string) string {
	return fmt.Sprintf("%s workstation %s", c.spec.verb, instanceID)
}

func defaultExecuteAction(rt globals.Runtime, verb string) func(context.Context, string) error {
	return func(ctx context.Context, instanceID string) error {
		if rt.Provider == nil {
			return fmt.Errorf("no provider configured; run 'fabrica setup' first")
		}

		manager, ok := rt.Provider.(fabricac.EC2InstanceManager)
		if !ok {
			return fmt.Errorf("provider does not support EC2 instance management; run 'fabrica setup' first")
		}

		switch verb {
		case startVerb:
			return manager.StartInstance(ctx, instanceID)
		case stopVerb:
			return manager.StopInstance(ctx, instanceID)
		default:
			return fmt.Errorf("unknown action verb: %s", verb)
		}
	}
}

func startSpec() spec {
	return spec{
		verb:         startVerb,
		short:        "Start a stopped cloud workstation EC2 instance",
		progressText: "Starting",
		targetStatus: "ready",
		long: `Start a previously stopped cloud workstation EC2 instance.

The workstation resumes from its saved state. DCV session setup may take
a minute or two after the instance comes online.

With --dry-run, shows what would happen without calling the EC2 API.`,
		alreadyActiveCode: "already_running",
		dryRunStatus:      "would_start",
		dryRunText:        "Would start the EC2 instance.",
		successText:       "started",
		followUpText:      "Run 'fabrica workstation list' to view connection details.",
		isAlreadyActive: func(status string) bool {
			return status == "ready" || status == "provisioning"
		},
		alreadyActiveText: func(instanceID, status string) string {
			return fmt.Sprintf("Instance %s is already running (status: %s)", instanceID, status)
		},
	}
}

func stopSpec() spec {
	return spec{
		verb:         stopVerb,
		short:        "Stop the cloud workstation EC2 instance",
		progressText: "Stopping",
		targetStatus: "stopped",
		long: `Stop the cloud workstation EC2 instance to pause billing.

The workstation's data and configuration are preserved. Use
'fabrica workstation start' to bring it back online.

With --dry-run, shows what would happen without calling the EC2 API.`,
		alreadyActiveCode: "already_stopped",
		dryRunStatus:      "would_stop",
		dryRunText:        "Would stop the EC2 instance.",
		successText:       "stopped",
		followUpText:      "Run 'fabrica workstation start' to bring it back online.",
		isAlreadyActive: func(status string) bool {
			return status == "stopped"
		},
		alreadyActiveText: func(instanceID, _ string) string {
			return fmt.Sprintf("Instance %s is already stopped.", instanceID)
		},
	}
}
