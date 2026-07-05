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
	lineWidth  = 64
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
		Example: `  # Show alias target, active fleet, and rollback candidates:
  fabrica deploy status

  # Machine-readable output for scripts:
  fabrica deploy status --json`,
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
		switch r.Properties["role"] {
		case "active":
			fc := f
			active = &fc
		case "superseded":
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
	fmt.Fprintf(c.out, "%s\n", summaryLine(alias.Identifier, active, candidates))
	fmt.Fprintf(c.out, "  Alias: %s\n", orDash(alias.Identifier))
	fmt.Fprintln(c.out)

	fmt.Fprintln(c.out, "Active fleet (alias points here):")
	if active != nil {
		fmt.Fprintf(c.out, "  %s %s  build=%s  status=%s\n",
			fleetSymbol(active.LiveStatus), active.FleetID, orDash(active.BuildVersion), active.LiveStatus)
	} else {
		fmt.Fprintln(c.out, "  (none) — run 'fabrica deploy promote <build-version>' to deploy a build")
	}
	fmt.Fprintln(c.out)

	fmt.Fprintln(c.out, "Rollback candidates (retained fleets):")
	if len(candidates) > 0 {
		for _, f := range candidates {
			fmt.Fprintf(c.out, "  %s %s  build=%s  status=%s   <- rollback target\n",
				fleetSymbol(f.LiveStatus), f.FleetID, orDash(f.BuildVersion), f.LiveStatus)
		}
	} else {
		fmt.Fprintln(c.out, "  (none) — only one fleet promoted so far")
	}

	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Next steps:")
	if active == nil {
		fmt.Fprintln(c.out, "  fabrica deploy promote <build-version>   Deploy a build to a new fleet")
	} else {
		fmt.Fprintln(c.out, "  fabrica deploy promote <build-version>   Roll out a new build (blue/green)")
	}
	if len(candidates) > 0 {
		fmt.Fprintln(c.out, "  fabrica deploy rollback                  Flip the alias to the newest retained fleet")
	}
	return nil
}

// summaryLine is the one-line headline at the top of the status output,
// mirroring the CI module's summary style.
func summaryLine(alias string, active *fleetJSON, candidates []fleetJSON) string {
	if alias == "" {
		return "deploy not fully set up — run 'fabrica deploy setup'"
	}
	if active == nil {
		return "alias ready • no fleet deployed yet"
	}
	s := fmt.Sprintf("alias ready • active fleet %s (%s)", active.FleetID, active.LiveStatus)
	if len(candidates) > 0 {
		s += fmt.Sprintf(" • %d rollback candidate(s)", len(candidates))
	}
	return s
}

// fleetSymbol maps a live GameLift fleet status to a fixed-width indicator,
// matching the [OK]/[....]/[FAIL] convention used by 'fabrica ci status'.
func fleetSymbol(status string) string {
	switch status {
	case "ACTIVE":
		return "[OK]  "
	case "NEW", "DOWNLOADING", "VALIDATING", "BUILDING", "ACTIVATING":
		return "[....]"
	case "ERROR", "DELETING", "TERMINATED":
		return "[FAIL]"
	default:
		return "[????]"
	}
}

// orDash renders an empty string as "(none)" for readable output.
func orDash(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
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
