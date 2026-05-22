package destroy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const (
	lineWidth  = 58
	moduleName = "perforce"
)

// DestroyOutput is the JSON-serialisable result of a destroy run.
type DestroyOutput struct {
	Destroyed []string `json:"destroyed"`
	Skipped   []string `json:"skipped,omitempty"`
	DryRun    bool     `json:"dryRun"`
}

type command struct {
	runtime   globals.Runtime
	dryRun    bool
	assumeYes bool
	jsonOut   bool
	out       io.Writer
	confirm   func(string, string) bool

	// seams for testing
	readState      func() (*fabricastate.State, error)
	writeState     func(*fabricastate.State) error
	deleteResource func(ctx context.Context, r *cloud.Resource) error
	getResource    func(ctx context.Context, r *cloud.Resource) error
}

// New returns the "perforce destroy" subcommand. It accepts RuntimeSource and
// OptionsSource closures so that global flags (--dry-run, --yes, --json) are
// resolved at execution time rather than at construction time.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Permanently delete the Perforce Helix Core server",
		Long: `Permanently delete the Perforce Helix Core server and all its AWS resources.

Resources are deleted in reverse-creation order to respect dependencies:
  1. EC2 Instance (terminated first)
  2. EC2 Security Group

State is updated after each deletion so a partial failure leaves a recoverable
record. Re-running destroy will skip resources that are already gone.

Before deleting the instance, the current EC2 state is checked:
  - stopping / shutting-down: destroy exits with an error; retry once complete.
  - terminated / not found: treated as already deleted; state is cleaned up.

With --dry-run, shows the destroy plan without making any AWS calls.`,
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
				c.deleteResource = rt.Provider.Resources().Delete
				c.getResource = rt.Provider.Resources().Get
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

	m := c.getPerforceModule(st)
	if m == nil {
		c.printNotProvisioned()
		return nil
	}

	resources := resourcesToDelete(m)

	if c.dryRun {
		c.printDryRun(m, resources)
		return nil
	}

	account := c.resolveAccount(st)

	if !c.jsonOut {
		c.printDestroyPlan(m, resources)
	}

	if !c.assumeYes {
		fmt.Fprintln(c.out)
		phrase := confirmPhrase(account)
		c.printConfirmInstructions(phrase)
		if !c.confirm("Enter confirmation phrase", phrase) {
			fmt.Fprintln(c.out, "Cancelled. No AWS calls were made.")
			return nil
		}
		fmt.Fprintln(c.out, "Confirmation accepted.")
	} else if !c.jsonOut {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "Proceeding without interactive confirmation (--yes flag set).")
	}

	return c.applyDestroy(ctx, st, m, resources)
}

func (c command) applyDestroy(ctx context.Context, st *fabricastate.State, m *fabricastate.ModuleState, resources []cloud.Resource) error {
	if c.deleteResource == nil {
		return fmt.Errorf("no provider configured; re-run after 'fabrica setup'")
	}

	if !c.jsonOut {
		fmt.Fprintln(c.out)
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
				removeResource(m, r.TypeName)
				st.UpsertModule(moduleName, m.Version, "destroying", m.Resources)
				if err := c.writeState(st); err != nil {
					fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
				}
				continue
			}
		}

		if !c.jsonOut {
			fmt.Fprintf(c.out, "Deleting %s %s...\n", r.TypeName, r.Identifier)
		}

		if err := c.deleteResource(ctx, &r); err != nil {
			if errors.Is(err, cloud.ErrResourceNotFound) {
				if !c.jsonOut {
					fmt.Fprintf(c.out, "  Already deleted: %s\n", r.Identifier)
				}
			} else {
				return fmt.Errorf("deleting %s %s: %w", r.TypeName, r.Identifier, err)
			}
		} else if !c.jsonOut {
			fmt.Fprintf(c.out, "  Deleted: %s\n", r.Identifier)
		}
		destroyed = append(destroyed, r.Identifier)

		// Remove this resource from state immediately so partial failure is recoverable.
		removeResource(m, r.TypeName)
		st.UpsertModule(moduleName, m.Version, "destroying", m.Resources)
		if err := c.writeState(st); err != nil {
			fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
		}
	}

	// All resources gone — remove the module from state entirely.
	removeModule(st, moduleName)
	if err := c.writeState(st); err != nil {
		fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
	}

	if c.jsonOut {
		c.printJSON(DestroyOutput{Destroyed: destroyed, DryRun: false})
		return nil
	}

	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Perforce Helix Core destroyed.")
	for _, id := range destroyed {
		fmt.Fprintf(c.out, "  Deleted: %s\n", id)
	}
	return nil
}

// checkInstanceBeforeDelete calls Get on the EC2 instance to detect transitional
// or already-terminated states before attempting a delete call.
// Returns (skip=true, nil) when the instance is already gone (terminated or not found).
// Returns (false, error) when the instance is in a transitional state.
// Returns (false, nil) when the delete should proceed normally.
func (c command) checkInstanceBeforeDelete(ctx context.Context, r *cloud.Resource) (skip bool, err error) {
	if c.getResource == nil {
		return false, nil
	}
	if err := c.getResource(ctx, r); err != nil {
		if errors.Is(err, cloud.ErrResourceNotFound) {
			if !c.jsonOut {
				fmt.Fprintf(c.out, "  Already deleted: %s\n", r.Identifier)
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
		return false, fmt.Errorf("instance %s is in transitional state %q — wait for it to finish and retry destroy", r.Identifier, actual.State.Name)
	case "terminated":
		if !c.jsonOut {
			fmt.Fprintf(c.out, "  Already deleted: %s\n", r.Identifier)
		}
		return true, nil
	}
	return false, nil
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

func confirmPhrase(account string) string {
	return fmt.Sprintf("destroy perforce %s", account)
}

func (c command) getPerforceModule(st *fabricastate.State) *fabricastate.ModuleState {
	return st.GetModule(moduleName)
}

func (c command) resolveAccount(st *fabricastate.State) string {
	if c.runtime.Config != nil && c.runtime.Config.Cloud.AWS.AccountID != "" {
		return c.runtime.Config.Cloud.AWS.AccountID
	}
	return st.Account
}

func (c command) printNotProvisioned() {
	if c.jsonOut {
		c.printJSON(DestroyOutput{Destroyed: []string{}, DryRun: c.dryRun})
		return
	}
	fmt.Fprintln(c.out, "Perforce is not provisioned. Nothing to destroy.")
}

func (c command) printDryRun(m *fabricastate.ModuleState, resources []cloud.Resource) {
	if c.jsonOut {
		ids := make([]string, len(resources))
		for i, r := range resources {
			ids[i] = r.Identifier
		}
		c.printJSON(DestroyOutput{Destroyed: ids, DryRun: true})
		return
	}
	fmt.Fprintln(c.out, "Perforce Helix Core (destroy dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Version:  %s\n", m.Version)
	fmt.Fprintf(c.out, "  Status:   %s\n", m.Status)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Resources that would be deleted (in order):")
	for i, r := range resources {
		fmt.Fprintf(c.out, "  %d. %s: %s\n", i+1, r.TypeName, r.Identifier)
	}
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Run without --dry-run to proceed.")
}

func (c command) printDestroyPlan(m *fabricastate.ModuleState, resources []cloud.Resource) {
	fmt.Fprintln(c.out, "Perforce Helix Core — destroy plan")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Version:  %s\n", m.Version)
	fmt.Fprintf(c.out, "  Status:   %s\n", m.Status)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Resources to delete (in order):")
	for i, r := range resources {
		fmt.Fprintf(c.out, "  %d. %s: %s\n", i+1, r.TypeName, r.Identifier)
	}
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "IRREVERSIBLE: This will permanently delete the Perforce server and its data.")
}

func (c command) printConfirmInstructions(phrase string) {
	fmt.Fprintln(c.out, "Confirmation required.")
	fmt.Fprintln(c.out, "Type this exact phrase to continue:")
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "  %s\n", phrase)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Any other input cancels.")
}

func (c command) printJSON(out DestroyOutput) {
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
