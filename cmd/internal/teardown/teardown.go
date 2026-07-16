// Package teardown is the shared engine behind the module destroy/terminate
// commands (perforce destroy, horde destroy, workstation terminate). Those
// commands are identical except for presentational strings and the verb, so
// they build a Spec and delegate to Command.Run.
package teardown

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

const lineWidth = 58

// Output is the JSON-serialisable result of a teardown run.
type Output struct {
	Destroyed []string `json:"destroyed"`
	Skipped   []string `json:"skipped,omitempty"`
	DryRun    bool     `json:"dryRun"`
}

// Spec holds the per-module presentation that varies between destroy/terminate.
// Everything else about a teardown is identical across modules.
type Spec struct {
	// ModuleName is the state module key (e.g. "perforce", "workstation").
	ModuleName string
	// Verb is the action word used in confirm phrases and transitional-state
	// errors (e.g. "destroy", "terminate").
	Verb string
	// VersionLabel labels the module version line in plan/dry-run output
	// (e.g. "Version" for perforce, "AMI ID" for horde/workstation).
	VersionLabel string
	// Title is the resource heading (e.g. "Perforce Helix Core").
	Title string
	// NotProvisioned is the message shown when the module has no state
	// (e.g. "Perforce is not provisioned. Nothing to destroy.").
	NotProvisioned string
	// PlanHeader heads the destroy/terminate plan (e.g. "Perforce Helix Core — destroy plan").
	PlanHeader string
	// DryRunHeader heads the dry-run output (e.g. "Perforce Helix Core (destroy dry run)").
	DryRunHeader string
	// Irreversible is the final warning line on the plan.
	Irreversible string
	// SuccessMessage is printed after a successful teardown (e.g. "Perforce Helix Core destroyed.").
	SuccessMessage string
	// ResourceOrder, when non-nil, returns the resources to delete in the order
	// they should be deleted. When nil, the engine uses the default EC2
	// Instance -> SecurityGroup order. Modules whose resources are not the
	// EC2/SG pair (e.g. deploy's GameLift fleet/build/alias/role) set this.
	ResourceOrder func(*fabricastate.ModuleState) []cloud.Resource
}

// Command runs a teardown for one module. The varying strings come from Spec;
// the func fields are seams the cmd layer wires to real implementations and
// tests replace with fakes.
type Command struct {
	Spec    Spec
	Runtime globals.Runtime

	DryRun    bool
	AssumeYes bool
	JSONOut   bool
	Out       io.Writer

	// SkipConfirm, when true, bypasses the interactive confirmation and the
	// standalone plan/confirmation output — used when an orchestrator (destroy
	// --all) has already confirmed the aggregate operation.
	SkipConfirm bool

	Confirm        func(string, string) bool
	ReadState      func() (*fabricastate.State, error)
	WriteState     func(*fabricastate.State) error
	DeleteResource func(ctx context.Context, r *cloud.Resource) error
	GetResource    func(ctx context.Context, r *cloud.Resource) error
}

// Run executes the teardown: read state, plan, confirm, then delete resources
// in reverse-creation order, persisting state after each deletion so a partial
// failure leaves a recoverable record.
func (c Command) Run(ctx context.Context) error {
	st, err := c.ReadState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	m := st.GetModule(c.Spec.ModuleName)
	if m == nil {
		c.printNotProvisioned()
		return nil
	}

	resources := resourcesToDelete2(c.Spec, m)

	if c.DryRun {
		c.printDryRun(m, resources)
		return nil
	}

	if !c.SkipConfirm {
		if !c.JSONOut {
			c.printPlan(m, resources)
		}

		if !c.AssumeYes {
			account := c.resolveAccount(st)
			fmt.Fprintln(c.Out)
			phrase := c.confirmPhrase(account)
			c.printConfirmInstructions(phrase)
			if !c.Confirm("Enter confirmation phrase", phrase) {
				fmt.Fprintln(c.Out, "Cancelled. No AWS calls were made.")
				return nil
			}
			fmt.Fprintln(c.Out, "Confirmation accepted.")
		} else if !c.JSONOut {
			fmt.Fprintln(c.Out)
			fmt.Fprintln(c.Out, "Proceeding without interactive confirmation (--yes flag set).")
		}
	}

	return c.apply(ctx, st, m, resources)
}

func (c Command) apply(ctx context.Context, st *fabricastate.State, m *fabricastate.ModuleState, resources []cloud.Resource) error {
	if c.DeleteResource == nil {
		return fmt.Errorf("no provider configured; run 'fabrica setup' first")
	}

	if !c.JSONOut {
		fmt.Fprintln(c.Out)
	}

	destroyed := make([]string, 0, len(resources))

	for _, res := range resources {
		r := res // copy for mutation

		if r.TypeName == "AWS::EC2::Instance" {
			skip, err := c.checkInstanceBeforeDelete(ctx, &r)
			if err != nil {
				return err
			}
			if skip {
				// Instance already gone — remove from state and continue.
				c.removeResourceAndPersist(st, m, r.TypeName)
				continue
			}
		}

		if !c.JSONOut {
			fmt.Fprintf(c.Out, "Deleting %s %s...\n", r.TypeName, r.Identifier)
		}

		if err := c.DeleteResource(ctx, &r); err != nil {
			if errors.Is(err, cloud.ErrResourceNotFound) {
				if !c.JSONOut {
					fmt.Fprintf(c.Out, "  Already deleted: %s\n", r.Identifier)
				}
			} else {
				return fmt.Errorf("deleting %s %s: %w", r.TypeName, r.Identifier, err)
			}
		} else if !c.JSONOut {
			fmt.Fprintf(c.Out, "  Deleted: %s\n", r.Identifier)
		}
		destroyed = append(destroyed, r.Identifier)

		// Remove this resource from state immediately so partial failure is recoverable.
		c.removeResourceAndPersist(st, m, r.TypeName)
	}

	// All resources gone — remove the module from state entirely.
	removeModule(st, c.Spec.ModuleName)
	if err := c.WriteState(st); err != nil {
		fmt.Fprintf(c.Out, "Warning: could not update local state: %v\n", err)
	}

	if c.JSONOut {
		c.printJSON(Output{Destroyed: destroyed, DryRun: false})
		return nil
	}

	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, c.Spec.SuccessMessage)
	for _, id := range destroyed {
		fmt.Fprintf(c.Out, "  Deleted: %s\n", id)
	}
	return nil
}

// removeResourceAndPersist drops a resource from the module and writes state,
// surfacing a write failure as a warning (state persistence is best-effort).
func (c Command) removeResourceAndPersist(st *fabricastate.State, m *fabricastate.ModuleState, typeName string) {
	removeResource(m, typeName)
	st.UpsertModule(c.Spec.ModuleName, m.Version, "destroying", m.Resources)
	if err := c.WriteState(st); err != nil {
		fmt.Fprintf(c.Out, "Warning: could not update local state: %v\n", err)
	}
}

// checkInstanceBeforeDelete calls Get on the EC2 instance to detect transitional
// or already-terminated states before attempting a delete call.
// Returns (skip=true, nil) when the instance is already gone (terminated or not found).
// Returns (false, error) when the instance is in a transitional state.
// Returns (false, nil) when the delete should proceed normally.
func (c Command) checkInstanceBeforeDelete(ctx context.Context, r *cloud.Resource) (skip bool, err error) {
	if c.GetResource == nil {
		return false, nil
	}
	if err := c.GetResource(ctx, r); err != nil {
		if errors.Is(err, cloud.ErrResourceNotFound) {
			if !c.JSONOut {
				fmt.Fprintf(c.Out, "  Already deleted: %s\n", r.Identifier)
			}
			return true, nil
		}
		return false, fmt.Errorf("querying instance %s before delete: %w", r.Identifier, err)
	}
	if len(r.ActualState) == 0 {
		return false, nil
	}
	var actual struct {
		State struct {
			Name string `json:"Name"`
		} `json:"State"`
	}
	if err := json.Unmarshal(r.ActualState, &actual); err != nil {
		return false, nil
	}
	switch actual.State.Name {
	case "stopping", "shutting-down":
		return false, fmt.Errorf("instance %s is in transitional state %q — wait for it to finish and retry %s", r.Identifier, actual.State.Name, c.Spec.Verb)
	case "terminated":
		if !c.JSONOut {
			fmt.Fprintf(c.Out, "  Already deleted: %s\n", r.Identifier)
		}
		return true, nil
	}
	return false, nil
}

func (c Command) confirmPhrase(account string) string {
	return fmt.Sprintf("%s %s %s", c.Spec.Verb, c.Spec.ModuleName, account)
}

func (c Command) resolveAccount(st *fabricastate.State) string {
	if c.Runtime.Config != nil && c.Runtime.Config.Cloud.AWS.AccountID != "" {
		return c.Runtime.Config.Cloud.AWS.AccountID
	}
	return st.Account
}

func (c Command) printNotProvisioned() {
	if c.JSONOut {
		c.printJSON(Output{Destroyed: []string{}, DryRun: c.DryRun})
		return
	}
	fmt.Fprintln(c.Out, c.Spec.NotProvisioned)
}

func (c Command) printDryRun(m *fabricastate.ModuleState, resources []cloud.Resource) {
	if c.JSONOut {
		ids := make([]string, len(resources))
		for i, r := range resources {
			ids[i] = r.Identifier
		}
		c.printJSON(Output{Destroyed: ids, DryRun: true})
		return
	}
	fmt.Fprintln(c.Out, c.Spec.DryRunHeader)
	fmt.Fprintln(c.Out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.Out, "  %-8s  %s\n", c.Spec.VersionLabel+":", m.Version)
	fmt.Fprintf(c.Out, "  Status:   %s\n", m.Status)
	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, "Resources that would be deleted (in order):")
	for i, r := range resources {
		fmt.Fprintf(c.Out, "  %d. %s: %s\n", i+1, r.TypeName, r.Identifier)
	}
	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, "Run without --dry-run to proceed.")
}

func (c Command) printPlan(m *fabricastate.ModuleState, resources []cloud.Resource) {
	fmt.Fprintln(c.Out, c.Spec.PlanHeader)
	fmt.Fprintln(c.Out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.Out, "  %-8s  %s\n", c.Spec.VersionLabel+":", m.Version)
	fmt.Fprintf(c.Out, "  Status:   %s\n", m.Status)
	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, "Resources to delete (in order):")
	for i, r := range resources {
		fmt.Fprintf(c.Out, "  %d. %s: %s\n", i+1, r.TypeName, r.Identifier)
	}
	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, c.Spec.Irreversible)
}

func (c Command) printConfirmInstructions(phrase string) {
	fmt.Fprintln(c.Out, "Confirmation required.")
	fmt.Fprintln(c.Out, "Type this exact phrase to continue:")
	fmt.Fprintln(c.Out)
	fmt.Fprintf(c.Out, "  %s\n", phrase)
	fmt.Fprintln(c.Out)
	fmt.Fprintln(c.Out, "Any other input cancels.")
}

func (c Command) printJSON(out Output) {
	data, _ := json.MarshalIndent(out, "", "  ")
	fmt.Fprintln(c.Out, string(data))
}

// resourcesToDelete returns resources in reverse-creation order: Instance → SG.
func resourcesToDelete(m *fabricastate.ModuleState) []cloud.Resource {
	var instance, sg *fabricastate.ModuleResource
	for i := range m.Resources {
		switch m.Resources[i].TypeName {
		case "AWS::EC2::Instance":
			instance = &m.Resources[i]
		case "AWS::EC2::SecurityGroup":
			sg = &m.Resources[i]
		}
	}

	var out []cloud.Resource
	if instance != nil {
		out = append(out, cloud.Resource{TypeName: instance.TypeName, Identifier: instance.Identifier})
	}
	if sg != nil {
		out = append(out, cloud.Resource{TypeName: sg.TypeName, Identifier: sg.Identifier})
	}
	return out
}

// resourcesToDelete2 returns the deletion-ordered resources for a module. If the
// Spec supplies a ResourceOrder hook it drives the order; otherwise the default
// EC2 Instance -> SecurityGroup order is used.
func resourcesToDelete2(spec Spec, m *fabricastate.ModuleState) []cloud.Resource {
	if spec.ResourceOrder != nil {
		return spec.ResourceOrder(m)
	}
	return resourcesToDelete(m)
}

func removeResource(m *fabricastate.ModuleState, typeName string) {
	filtered := m.Resources[:0]
	for _, r := range m.Resources {
		if r.TypeName != typeName {
			filtered = append(filtered, r)
		}
	}
	m.Resources = filtered
}

func removeModule(st *fabricastate.State, name string) {
	filtered := st.Modules[:0]
	for _, m := range st.Modules {
		if m.Name != name {
			filtered = append(filtered, m)
		}
	}
	st.Modules = filtered
}
