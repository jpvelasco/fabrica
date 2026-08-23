// Package destroy provides the "horde agents destroy" subcommand.
package destroy

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/oplog"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const (
	moduleName = "horde"
	lineWidth  = 58
	agentRole  = "agent"
)

// isAgentResource returns true if the resource belongs to the agent pool.
// Agent resources are marked with Properties["role"] = "agent" at creation
// time, so identification is properties-based rather than prefix-based
// (Cloud Control returns physical IDs like sg-*, lt-*, not logical names).
func isAgentResource(r fabricastate.ModuleResource) bool {
	return r.Properties != nil && r.Properties["role"] == agentRole
}

// agentsToDelete returns agent resources in deletion order.
func agentsToDelete(m *fabricastate.ModuleState) []cloud.Resource {
	// Ingress → scaling policy → alarms → ASG → LT → instance profile →
	// role → SG. The ingress rule references both the coordinator and agent
	// security groups, so it must go before either SG; scaling resources go
	// before the ASG they reference.
	order := []string{
		"AWS::EC2::SecurityGroupIngress",
		"AWS::AutoScaling::ScalingPolicy",
		"AWS::CloudWatch::Alarm",
		"AWS::AutoScaling::AutoScalingGroup",
		"AWS::EC2::LaunchTemplate",
		"AWS::IAM::InstanceProfile",
		"AWS::IAM::Role",
		"AWS::EC2::SecurityGroup",
	}
	out := make([]cloud.Resource, 0, len(order))
	for _, t := range order {
		for _, r := range m.Resources {
			if r.TypeName == t && isAgentResource(r) && r.Identifier != "" {
				out = append(out, cloud.Resource{TypeName: r.TypeName, Identifier: r.Identifier})
			}
		}
	}
	return out
}

// agentsProvisioned returns true if any agent resources exist in the horde module.
func agentsProvisioned(m *fabricastate.ModuleState) bool {
	for _, r := range m.Resources {
		if isAgentResource(r) {
			return true
		}
	}
	return false
}

type command struct {
	runtime        globals.Runtime
	dryRun         bool
	assumeYes      bool
	jsonOut        bool
	out            io.Writer
	confirm        func(string, string) bool
	readState      func() (*fabricastate.State, error)
	writeState     func(*fabricastate.State) error
	deleteResource func(ctx context.Context, r *cloud.Resource) error
	getResource    func(ctx context.Context, r *cloud.Resource) error
}

// New returns the "horde agents destroy" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destroy",
		Short: "Permanently delete the Horde agent pool",
		Long: `Permanently delete the Horde build agent pool and all its AWS resources.

Resources are deleted in reverse-creation order to respect dependencies:
  1. Scaling Policies (scale-out and scale-in, if enabled)
  2. CloudWatch Alarms (scale-out and scale-in, if enabled)
  3. Auto Scaling Group (scaled to 0 and deleted)
  4. Launch Template
  5. IAM Instance Profile
  6. IAM Role
  7. Security Group

This removes only the agent pool resources. The Horde coordinator and its
resources are not affected. Use 'fabrica horde destroy' to remove the
coordinator as well.

State is updated after each deletion so a partial failure leaves a recoverable
record. Re-running destroy will skip resources that are already gone.

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
			c.readState = func() (*fabricastate.State, error) { return provision.ReadState(rt) }
			c.writeState = fabricastate.WriteState
			if rt.Provider != nil {
				c.deleteResource = rt.Provider.Resources().Delete
				c.getResource = rt.Provider.Resources().Get
			}
			return c.run(cmd.Context())
		},
	}
	return cmd
}

func (c *command) run(ctx context.Context) error {
	ctx, releaseLock, err := provision.AcquireStateLock(ctx, c.runtime, "horde agents destroy")
	if err != nil {
		return err
	}
	defer releaseLock()

	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	m := st.GetModule(moduleName)
	if m == nil || !agentsProvisioned(m) {
		c.printNotProvisioned()
		return nil
	}

	resources := agentsToDelete(m)
	if len(resources) == 0 {
		c.printNotProvisioned()
		return nil
	}

	if c.dryRun {
		c.printDryRun(m, resources)
		return nil
	}

	c.printPlan(m, resources)

	if c.assumeYes {
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "Proceeding without interactive confirmation (--yes flag set).")
	} else {
		account := c.resolveAccount(st)
		phrase := fmt.Sprintf("destroy agents %s", account)
		fmt.Fprintln(c.out)
		provision.PrintConfirmInstructions(c.out, phrase)
		if !c.confirm("Enter confirmation phrase", phrase) {
			fmt.Fprintln(c.out, "Cancelled. No AWS calls were made.")
			return nil
		}
		fmt.Fprintln(c.out, "Confirmation accepted.")
	}

	return c.apply(ctx, st, m, resources)
}

func (c *command) apply(ctx context.Context, st *fabricastate.State, m *fabricastate.ModuleState, resources []cloud.Resource) error {
	if c.deleteResource == nil {
		return fmt.Errorf("no provider configured; run 'fabrica setup' first")
	}

	fmt.Fprintln(c.out)

	// deleteOneResource marks the module "destroying" through the aliased
	// state entry; remember the pre-destroy status so it can be restored.
	origStatus := m.Status

	destroyed := make([]string, 0, len(resources))
	for _, res := range resources {
		if err := c.deleteOneResource(ctx, st, m, res); err != nil {
			return err
		}
		destroyed = append(destroyed, res.Identifier)
	}

	// Update module status but keep the module (coordinator still exists).
	st.UpsertModule(moduleName, m.Version, origStatus, m.Resources)
	if err := c.writeState(st); err != nil {
		fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
	}

	if c.jsonOut {
		modstatus.WriteJSON(c.out, teardown.Output{Destroyed: destroyed, DryRun: false})
		return nil
	}

	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Horde build agent pool destroyed.")
	for _, id := range destroyed {
		fmt.Fprintf(c.out, "  Deleted: %s\n", id)
	}
	return nil
}

func (c *command) deleteOneResource(ctx context.Context, st *fabricastate.State, m *fabricastate.ModuleState, res cloud.Resource) error {
	r := res // copy for mutation

	if !c.jsonOut {
		fmt.Fprintf(c.out, "Deleting %s %s...\n", r.TypeName, r.Identifier)
	}
	oplog.WithModule("horde-agents").Debug("deleting resource", "type", r.TypeName, "identifier", r.Identifier)

	if err := c.deleteResource(ctx, &r); err == nil {
		if !c.jsonOut {
			fmt.Fprintf(c.out, "  Deleted: %s\n", r.Identifier)
		}
		oplog.WithModule("horde-agents").Debug("resource deleted", "identifier", r.Identifier)
	} else {
		if err != nil && strings.Contains(err.Error(), "not found") {
			if !c.jsonOut {
				fmt.Fprintf(c.out, "  Already deleted: %s\n", r.Identifier)
			}
			oplog.WithModule("horde-agents").Debug("resource already deleted", "identifier", r.Identifier)
		} else {
			return fmt.Errorf("deleting %s %s: %w", r.TypeName, r.Identifier, err)
		}
	}

	// Remove only this specific resource from state.
	removeResource(m, r.TypeName, r.Identifier)
	st.UpsertModule(moduleName, m.Version, "destroying", m.Resources)
	if err := c.writeState(st); err != nil {
		fmt.Fprintf(c.out, "Warning: could not update local state: %v\n", err)
	}
	return nil
}

func (c *command) printNotProvisioned() {
	if c.jsonOut {
		modstatus.WriteJSON(c.out, teardown.Output{Destroyed: []string{}, DryRun: c.dryRun})
		return
	}
	fmt.Fprintln(c.out, "Horde agents are not provisioned. Nothing to destroy.")
}

func (c *command) printDryRun(m *fabricastate.ModuleState, resources []cloud.Resource) {
	if c.jsonOut {
		ids := make([]string, 0, len(resources))
		for _, r := range resources {
			ids = append(ids, r.Identifier)
		}
		modstatus.WriteJSON(c.out, teardown.Output{Destroyed: ids, DryRun: true})
		return
	}
	fmt.Fprintln(c.out, "Horde build agent pool (destroy dry run)")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Status:   %s\n", m.Status)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Resources that would be deleted (in order):")
	for i, r := range resources {
		fmt.Fprintf(c.out, "  %d. %s: %s\n", i+1, r.TypeName, r.Identifier)
	}
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Run without --dry-run to proceed.")
}

func (c *command) printPlan(m *fabricastate.ModuleState, resources []cloud.Resource) {
	if c.jsonOut {
		return
	}
	fmt.Fprintln(c.out, "Horde build agent pool — destroy plan")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Status:   %s\n", m.Status)
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Resources to delete (in order):")
	for i, r := range resources {
		fmt.Fprintf(c.out, "  %d. %s: %s\n", i+1, r.TypeName, r.Identifier)
	}
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "IRREVERSIBLE: This will permanently delete the agent pool, launch template, IAM role/profile, and security group.")
}

func (c *command) resolveAccount(st *fabricastate.State) string {
	if c.runtime.Config != nil && c.runtime.Config.Cloud.AWS.AccountID != "" {
		return c.runtime.Config.Cloud.AWS.AccountID
	}
	return st.Account
}

// removeResource drops the resource with the given (typeName, identifier) from
// the module's resource list.
func removeResource(m *fabricastate.ModuleState, typeName, identifier string) {
	filtered := m.Resources[:0]
	for _, r := range m.Resources {
		if r.TypeName == typeName && r.Identifier == identifier {
			continue
		}
		filtered = append(filtered, r)
	}
	m.Resources = filtered
}

// NewTeardown builds this module's teardown.Command for orchestrated use by
// `fabrica destroy --all`. The full horde destroy (via hordedestroy) already
// handles agent resources, so this returns a no-op for the orchestrator.
func NewTeardown(_ globals.Runtime, _ io.Writer) teardown.Command {
	// Agents are torn down as part of the full horde destroy.
	// The orchestrator calls hordedestroy.NewTeardown which covers both
	// coordinator and agent resources. Return a no-op command here.
	return teardown.Command{}
}
