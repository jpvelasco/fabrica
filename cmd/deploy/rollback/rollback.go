// Package rollback implements "fabrica deploy rollback": flip the GameLift alias
// back to the most-recent superseded (retained) fleet.
package rollback

import (
	"context"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/deploy"
	"github.com/jpvelasco/fabrica/internal/prompt"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const moduleName = "deploy"

type command struct {
	runtime   globals.Runtime
	assumeYes bool
	out       io.Writer

	readState      func() (*fabricastate.State, error)
	writeState     func(*fabricastate.State) error
	updateResource func(ctx context.Context, r *cloud.Resource) error
	fleetStatus    func(ctx context.Context, fleetID string) (cloud.FleetInfo, error)
	confirm        func(string) bool
}

// New returns the "deploy rollback" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "rollback",
		Short: "Flip the alias back to the previous (retained) fleet",
		Long: `Roll back the deployment by flipping the GameLift alias to the most-recent
retained ("superseded") fleet. The target fleet must still be ACTIVE.

Use this when a freshly-promoted build misbehaves: the previous fleet is kept
running by 'deploy promote' precisely so rollback is instant.`,
		Example: `  fabrica deploy rollback
  fabrica deploy rollback --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				runtime:    rt,
				assumeYes:  opts.AssumeYes,
				out:        out,
				readState:  func() (*fabricastate.State, error) { return provision.ReadState(rt) },
				writeState: fabricastate.WriteState,
				confirm:    prompt.Confirm,
			}
			if rt.Provider != nil {
				if rc := rt.Provider.Resources(); rc != nil {
					c.updateResource = rc.Update
				}
				if glm, ok := rt.Provider.(cloud.GameLiftManager); ok {
					c.fleetStatus = glm.FleetStatus
				}
			}
			return c.run(cmd.Context())
		},
	}
}

func (c command) run(ctx context.Context) error {
	if c.runtime.Provider == nil {
		return fmt.Errorf("no cloud provider configured — check your config and credentials")
	}
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	m := st.GetModule(moduleName)
	if m == nil {
		return fmt.Errorf("deploy is not set up. Run 'fabrica deploy setup' first")
	}
	alias, ok := stateutil.ResourceByType(m, deploy.TypeGameLiftAlias)
	if !ok {
		return fmt.Errorf("deploy alias not found in state. Run 'fabrica deploy setup' first")
	}

	active, target := findActiveAndSuperseded(m)
	if target == "" {
		return fmt.Errorf("no previous fleet to roll back to — only one fleet has been promoted. Nothing to do")
	}

	if c.fleetStatus == nil {
		return fmt.Errorf("cloud provider does not support GameLift — only AWS is supported in V1")
	}
	info, err := c.fleetStatus(ctx, target)
	if err != nil {
		return fmt.Errorf("checking rollback target fleet %s: %w", target, err)
	}
	if info.Status != "ACTIVE" {
		return fmt.Errorf("rollback target fleet %s is %s, not ACTIVE — it may have been terminated; cannot roll back to it", target, info.Status)
	}

	fmt.Fprintf(c.out, "Rolling back the alias:\n")
	fmt.Fprintf(c.out, "  current fleet: %s\n", active)
	fmt.Fprintf(c.out, "  target fleet:  %s\n", target)
	fmt.Fprintln(c.out)

	if !c.assumeYes {
		if !c.confirm(fmt.Sprintf("Flip alias %s to fleet %s?", alias.Identifier, target)) {
			fmt.Fprintln(c.out, "Rollback cancelled. The alias was not changed.")
			return nil
		}
	}

	patch, err := deploy.AliasFlipPatch(target)
	if err != nil {
		return fmt.Errorf("building alias flip patch: %w", err)
	}
	r := &cloud.Resource{TypeName: deploy.TypeGameLiftAlias, Identifier: alias.Identifier, DesiredState: patch}
	if err := c.updateResource(ctx, r); err != nil {
		return fmt.Errorf("flipping alias %s to fleet %s: %w", alias.Identifier, target, err)
	}

	swapRoles(m, target, active)
	if err := c.writeState(st); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}
	fmt.Fprintf(c.out, "Rolled back — alias %s now points to fleet %s.\n", alias.Identifier, target)
	return nil
}

// findActiveAndSuperseded returns the identifiers of the active fleet and the
// most-recent superseded fleet (the last one in resource order).
func findActiveAndSuperseded(m *fabricastate.ModuleState) (active, superseded string) {
	for _, r := range m.Resources {
		if r.TypeName != deploy.TypeGameLiftFleet {
			continue
		}
		switch r.Properties["role"] {
		case "active":
			active = r.Identifier
		case "superseded":
			superseded = r.Identifier // later entries win → most recent
		}
	}
	return active, superseded
}

// swapRoles makes target active and the former active superseded.
func swapRoles(m *fabricastate.ModuleState, target, formerActive string) {
	for i := range m.Resources {
		r := &m.Resources[i]
		if r.TypeName != deploy.TypeGameLiftFleet {
			continue
		}
		if r.Properties == nil {
			r.Properties = map[string]string{}
		}
		switch r.Identifier {
		case target:
			r.Properties["role"] = "active"
		case formerActive:
			r.Properties["role"] = "superseded"
		}
	}
}
