// Package status implements "fabrica ci status": show the CI infrastructure and
// the status of the most recent build.
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
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const (
	moduleName  = "ci"
	lineWidth   = 64
	projectType = "AWS::CodeBuild::Project"
	roleType    = "AWS::IAM::Role"
)

// StatusOutput is the JSON view of CI status.
type StatusOutput struct {
	Provisioned bool   `json:"provisioned"`
	Project     string `json:"project,omitempty"`
	Role        string `json:"role,omitempty"`
	LastBuildID string `json:"lastBuildId,omitempty"`
	BuildStatus string `json:"buildStatus,omitempty"`
	BuildPhase  string `json:"buildPhase,omitempty"`
}

type command struct {
	runtime   globals.Runtime
	jsonOut   bool
	buildID   string
	out       io.Writer
	readState func() (*fabricastate.State, error)
	runner    cloud.CodeBuildRunner
}

// New returns the "ci status" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var buildID string
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show CI infrastructure and recent build status",
		Long: `Show the CI infrastructure (CodeBuild project + IAM role) from local state,
with [OK]/[WARN] indicators and a one-line summary. Read-only — never mutates state.

Pass --build <id> to also query the live status of a specific build (id from
'fabrica ci trigger' output).`,
		Example: `  # Show CI infrastructure status:
  fabrica ci status

  # Also show the live status of a specific build:
  fabrica ci status --build fabrica-ci:1a2b3c4d-...

  # Machine-readable output for scripts:
  fabrica ci status --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				runtime:   rt,
				jsonOut:   opts.JSONOutput,
				buildID:   buildID,
				out:       out,
				readState: func() (*fabricastate.State, error) { return provision.ReadState(rt) },
			}
			if rt.Provider != nil {
				if r, ok := rt.Provider.(cloud.CodeBuildRunner); ok {
					c.runner = r
				}
			}
			return c.run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&buildID, "build", "", "Build ID to show live status for")
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
			return c.printJSON(StatusOutput{Provisioned: false})
		}
		fmt.Fprintln(c.out, "CI is not provisioned. Run 'fabrica ci setup' to set it up.")
		return nil
	}

	out := StatusOutput{Provisioned: true}
	if proj, ok := stateutil.ResourceByType(m, projectType); ok {
		out.Project = proj.Identifier
	}
	if role, ok := stateutil.ResourceByType(m, roleType); ok {
		out.Role = role.Identifier
	}

	if c.buildID != "" && c.runner != nil {
		info, err := c.runner.BuildStatus(ctx, c.buildID)
		if err != nil {
			return fmt.Errorf("getting build status: %w", err)
		}
		out.LastBuildID = info.ID
		out.BuildStatus = info.Status
		out.BuildPhase = info.Phase
	}

	if c.jsonOut {
		return c.printJSON(out)
	}
	c.printText(out)
	return nil
}

func (c command) printJSON(o StatusOutput) error {
	data, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding status: %w", err)
	}
	fmt.Fprintln(c.out, string(data))
	return nil
}

func (c command) printText(o StatusOutput) {
	fmt.Fprintln(c.out, "Fabrica CI")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "%s\n", ciSummaryLine(o))
	fmt.Fprintln(c.out)

	fmt.Fprintf(c.out, "  %s CodeBuild project:  %s\n", presenceSymbol(o.Project), orDash(o.Project))
	fmt.Fprintf(c.out, "  %s IAM role:           %s\n", presenceSymbol(o.Role), orDash(o.Role))

	if o.BuildStatus != "" {
		fmt.Fprintln(c.out)
		fmt.Fprintf(c.out, "  %s Build %s\n", buildSymbol(o.BuildStatus), o.LastBuildID)
		fmt.Fprintf(c.out, "       status: %s  phase: %s\n", o.BuildStatus, orDash(o.BuildPhase))
	}

	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Next steps:")
	fmt.Fprintln(c.out, "  fabrica ci trigger <buildgraph.xml>   Run a BuildGraph job on Horde")
}

// ciSummaryLine is the one-line overview: whether the infra is ready and the
// latest build outcome (when a --build id was queried).
func ciSummaryLine(o StatusOutput) string {
	infra := "infrastructure ready"
	if o.Project == "" || o.Role == "" {
		infra = "infrastructure incomplete"
	}
	if o.BuildStatus == "" {
		return infra
	}
	return fmt.Sprintf("%s • last build %s", infra, o.BuildStatus)
}

// presenceSymbol is [OK] when a resource is recorded, [WARN] when it's missing.
func presenceSymbol(id string) string {
	if id == "" {
		return "[WARN]"
	}
	return "[OK]  "
}

// buildSymbol maps a CodeBuild status to a status indicator.
func buildSymbol(status string) string {
	switch status {
	case "SUCCEEDED":
		return "[OK]  "
	case "IN_PROGRESS":
		return "[....]"
	case "FAILED", "FAULT", "STOPPED", "TIMED_OUT":
		return "[FAIL]"
	default:
		return "[????]"
	}
}

func orDash(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
