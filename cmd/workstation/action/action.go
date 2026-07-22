// Package action provides the shared implementation for workstation start and stop.
// The start and stop commands are structurally identical except for the action verb,
// target status, and the already-active status check.
package action

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
)

const (
	lineWidth  = 58
	moduleName = "workstation"
)

// ActionOutput is the JSON-serialisable result of a start/stop run.
type ActionOutput struct {
	InstanceID string `json:"instanceId"`
	Status     string `json:"status"`
	DryRun     bool   `json:"dryRun"`
}

// Spec holds the varying parameters for start vs stop.
type Spec struct {
	ActionVerb          string
	ProgressText        string
	TargetStatus        string
	AlreadyActiveStatus string
	AlreadyActiveText   string
	DryRunStatus        string
	DryRunText          string
	SuccessText         string
	FollowUpText        string
	IsAlreadyActive     func(status string) bool
	ActionLabel         string
}

// Command is the shared implementation for workstation start/stop.
type Command struct {
	spec      Spec
	runtime   globals.Runtime
	dryRun    bool
	assumeYes bool
	jsonOut   bool
	out       io.Writer
	confirm   func(string, string) bool

	// seams for testing
	readState     func() (*fabricastate.State, error)
	writeState    func(*fabricastate.State) error
	executeAction func(ctx context.Context, instanceID string) error
}

// New creates a new shared start/stop command.
func New(spec Spec, runtime globals.Runtime, dryRun, assumeYes, jsonOut bool, out io.Writer, confirm func(string, string) bool, executeAction func(context.Context, string) error) *Command {
	return &Command{
		spec:          spec,
		runtime:       runtime,
		dryRun:        dryRun,
		assumeYes:     assumeYes,
		jsonOut:       jsonOut,
		out:           out,
		confirm:       confirm,
		executeAction: executeAction,
	}
}

// SetReadState sets the readState seam (for testing).
func (c *Command) SetReadState(fn func() (*fabricastate.State, error)) {
	c.readState = fn
}

// SetWriteState sets the writeState seam (for testing).
func (c *Command) SetWriteState(fn func(*fabricastate.State) error) {
	c.writeState = fn
}

func (c *Command) Run(ctx context.Context) error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	m := st.GetModule(moduleName)
	if m == nil {
		if c.jsonOut {
			c.printJSON(ActionOutput{Status: "not_provisioned", DryRun: c.dryRun})
			return nil
		}
		fmt.Fprintln(c.out, "Workstation is not provisioned. Nothing to "+c.spec.ActionVerb+".")
		return nil
	}

	instRes, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance")
	if !ok || instRes.Identifier == "" {
		return fmt.Errorf("workstation has no instance in state; run 'fabrica workstation list' to inspect")
	}
	instanceID := instRes.Identifier

	if c.spec.IsAlreadyActive(m.Status) {
		if c.jsonOut {
			c.printJSON(ActionOutput{InstanceID: instanceID, Status: c.spec.AlreadyActiveStatus, DryRun: c.dryRun})
			return nil
		}
		if strings.Contains(c.spec.AlreadyActiveText, "%s") {
			fmt.Fprintf(c.out, "Instance %s "+c.spec.AlreadyActiveText+"\n", instanceID, m.Status)
		} else {
			fmt.Fprintf(c.out, "Instance %s "+c.spec.AlreadyActiveText+".\n", instanceID)
		}
		return nil
	}

	if c.dryRun {
		c.printDryRun(m, instanceID)
		return nil
	}

	if !c.jsonOut {
		c.printPlan(m, instanceID)
	}

	if !c.assumeYes {
		fmt.Fprintln(c.out)
		phrase := c.confirmPhrase(instanceID)
		c.printConfirmInstructions(phrase)
		if !c.confirm("Enter confirmation phrase", phrase) {
			fmt.Fprintln(c.out, "Cancelled. No AWS calls were made.")
			return nil
		}
		fmt.Fprintln(c.out, "Confirmation accepted.")
	} else if !c.jsonOut {
		fmt.Fprintln(c.out, "Proceeding without interactive confirmation (--yes flag set).")
	}

	return c.apply(ctx, st, m, instanceID)
}

func (c *Command) apply(ctx context.Context, st *fabricastate.State, m *fabricastate.ModuleState, instanceID string) error {
	if c.executeAction == nil {
		return fmt.Errorf("no provider configured; run 'fabrica setup' first")
	}

	if !c.jsonOut {
		fmt.Fprintf(c.out, c.spec.ProgressText+" instance %s...\n", instanceID)
	}

	if err := c.executeAction(ctx, instanceID); err != nil {
		return fmt.Errorf(strings.ToLower(c.spec.ProgressText)+" instance %s: %w", instanceID, err)
	}

	st.UpsertModule(moduleName, m.Version, c.spec.TargetStatus, m.Resources)
	if err := c.writeState(st); err != nil {
		fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
	}

	if c.jsonOut {
		c.printJSON(ActionOutput{InstanceID: instanceID, Status: c.spec.TargetStatus, DryRun: false})
		return nil
	}

	fmt.Fprintf(c.out, "  Instance %s "+c.spec.SuccessText+".\n", instanceID)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, c.spec.FollowUpText)
	return nil
}

func (c *Command) printDryRun(m *fabricastate.ModuleState, instanceID string) {
	if c.jsonOut {
		c.printJSON(ActionOutput{InstanceID: instanceID, Status: c.spec.DryRunStatus, DryRun: true})
		return
	}
	fmt.Fprintln(c.out, "Cloud Workstation ("+c.spec.ActionLabel+" dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Instance ID: %s\n", instanceID)
	fmt.Fprintf(c.out, "  Status:      %s\n", m.Status)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, c.spec.DryRunText)
	fmt.Fprintln(c.out, "Run without --dry-run to proceed.")
}

func (c *Command) printPlan(m *fabricastate.ModuleState, instanceID string) {
	fmt.Fprintln(c.out, "Cloud Workstation — "+c.spec.ActionLabel)
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Instance ID: %s\n", instanceID)
	fmt.Fprintf(c.out, "  Status:      %s\n", m.Status)
	fmt.Fprintln(c.out)
}

func (c *Command) printJSON(out ActionOutput) {
	data, _ := json.MarshalIndent(out, "", "  ")
	fmt.Fprintln(c.out, string(data))
}

func (c *Command) confirmPhrase(instanceID string) string {
	return fmt.Sprintf("%s workstation %s", c.spec.ActionVerb, instanceID)
}

func (c *Command) printConfirmInstructions(phrase string) {
	fmt.Fprintln(c.out, "Confirmation required.")
	fmt.Fprintln(c.out, "Type this exact phrase to continue:")
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  %s\n", phrase)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Any other input cancels.")
}

// DefaultReadState returns the default readState implementation.
func (c *Command) DefaultReadState() (*fabricastate.State, error) {
	account, region := "", ""
	if c.runtime.Config != nil {
		account = c.runtime.Config.Cloud.AWS.AccountID
		region = c.runtime.Config.Cloud.AWS.Region
	}
	return fabricastate.ReadStateOrNew(account, region)
}

// DefaultWriteState returns the default writeState implementation.
func (c *Command) DefaultWriteState(st *fabricastate.State) error {
	return fabricastate.WriteState(st)
}

// StartSpec is the Spec for the start command.
var StartSpec = Spec{
	ActionVerb:          "start",
	ProgressText:        "Starting",
	TargetStatus:        "ready",
	AlreadyActiveStatus: "already_running",
	AlreadyActiveText:   "is already running (status: %s)",
	DryRunStatus:        "would_start",
	DryRunText:          "Would start the EC2 instance.",
	SuccessText:         "started",
	FollowUpText:        "Run 'fabrica workstation list' to view connection details.",
	ActionLabel:         "start",
	IsAlreadyActive: func(status string) bool {
		return status == "ready" || status == "provisioning"
	},
}

// StopSpec is the Spec for the stop command.
var StopSpec = Spec{
	ActionVerb:          "stop",
	ProgressText:        "Stopping",
	TargetStatus:        "stopped",
	AlreadyActiveStatus: "already_stopped",
	AlreadyActiveText:   "is already stopped",
	DryRunStatus:        "would_stop",
	DryRunText:          "Would stop the EC2 instance.",
	SuccessText:         "stopped",
	FollowUpText:        "Run 'fabrica workstation start' to bring it back online.",
	ActionLabel:         "stop",
	IsAlreadyActive: func(status string) bool {
		return status == "stopped"
	},
}
