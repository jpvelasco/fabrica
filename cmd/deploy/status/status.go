// Package status implements "fabrica deploy status": a read-only overview of the
// deploy module — the alias, the active fleet, and any retained rollback
// candidates, with live GameLift fleet status. Never mutates state.
package status

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/deploy"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const (
	moduleName = "deploy"
	lineWidth  = 58
)

type command struct {
	runtime globals.Runtime
	jsonOut bool
	out     io.Writer

	readState   func() (*fabricastate.State, error)
	fleetStatus func(ctx context.Context, fleetID string) (cloud.FleetInfo, error)
	fleetEvents func(ctx context.Context, fleetID string) ([]cloud.FleetEvent, error)
}

type fleetJSON struct {
	FleetID      string `json:"fleetId"`
	BuildVersion string `json:"buildVersion"`
	Role         string `json:"role"`
	LiveStatus   string `json:"liveStatus"`
}

type statusJSON struct {
	Provisioned        bool        `json:"provisioned"`
	Alias              string      `json:"alias,omitempty"`
	ActiveFleet        *fleetJSON  `json:"activeFleet,omitempty"`
	RollbackCandidates []fleetJSON `json:"rollbackCandidates,omitempty"`
}

// New returns the "deploy status" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show deploy status (alias, active fleet, rollback candidates)",
		Long: `Show the deploy module's current state: the GameLift alias and the fleet it
points to, plus any retained fleets you can roll back to. Queries live fleet
status from GameLift. Read-only — never changes anything.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				runtime:   rt,
				jsonOut:   opts.JSONOutput,
				out:       out,
				readState: func() (*fabricastate.State, error) { return provision.ReadState(rt) },
			}
			if rt.Provider != nil {
				if glm, ok := rt.Provider.(cloud.GameLiftManager); ok {
					c.fleetStatus = glm.FleetStatus
					c.fleetEvents = glm.FleetEvents
				}
			}
			return c.run(cmd.Context())
		},
	}
}

func (c command) run(ctx context.Context) error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	m := st.GetModule(moduleName)
	if m == nil {
		if c.jsonOut {
			return c.emitJSON(statusJSON{Provisioned: false})
		}
		fmt.Fprintln(c.out, "Deploy is not set up. Run 'fabrica deploy setup' to begin.")
		return nil
	}

	alias, _ := stateutil.ResourceByType(m, deploy.TypeGameLiftAlias)

	var active *fleetJSON
	var candidates []fleetJSON
	for _, r := range m.Resources {
		if r.TypeName != deploy.TypeGameLiftFleet {
			continue
		}
		f := fleetJSON{
			FleetID:      r.Identifier,
			BuildVersion: r.Properties["buildVersion"],
			Role:         r.Properties["role"],
			LiveStatus:   c.liveStatus(ctx, r.Identifier),
		}
		if r.Properties["role"] == "active" {
			fc := f
			active = &fc
		} else if r.Properties["role"] == "superseded" {
			candidates = append(candidates, f)
		}
	}

	if c.jsonOut {
		return c.emitJSON(statusJSON{
			Provisioned:        true,
			Alias:              alias.Identifier,
			ActiveFleet:        active,
			RollbackCandidates: candidates,
		})
	}

	fmt.Fprintln(c.out, "Deploy status")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "  Alias: %s\n", alias.Identifier)
	fmt.Fprintln(c.out)
	if active != nil {
		fmt.Fprintln(c.out, "Active fleet (alias points here):")
		fmt.Fprintf(c.out, "  %s  role=%s  build=%s  status=%s\n", active.FleetID, active.Role, active.BuildVersion, active.LiveStatus)
	} else {
		fmt.Fprintln(c.out, "No active fleet yet. Run 'fabrica deploy promote <build-version>'.")
	}
	fmt.Fprintln(c.out)
	if len(candidates) > 0 {
		fmt.Fprintln(c.out, "Rollback candidates (retained — 'fabrica deploy rollback' flips to the newest):")
		for _, f := range candidates {
			fmt.Fprintf(c.out, "  %s  role=%s  build=%s  status=%s   <- rollback candidate\n", f.FleetID, f.Role, f.BuildVersion, f.LiveStatus)
		}
	} else {
		fmt.Fprintln(c.out, "No rollback candidates (only one fleet promoted so far).")
	}
	return nil
}

// liveStatus queries GameLift for the fleet's current status, degrading to
// "unknown" if no provider/manager is available or the call fails (status is
// read-only and must never hard-fail the overview).
func (c command) liveStatus(ctx context.Context, fleetID string) string {
	if c.fleetStatus == nil {
		return "unknown (no provider)"
	}
	info, err := c.fleetStatus(ctx, fleetID)
	if err != nil {
		return "unknown"
	}
	return info.Status
}

func (c command) emitJSON(s statusJSON) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	fmt.Fprintln(c.out, string(data))
	return nil
}
