// Package status implements `fabrica status`: a read-only aggregate overview of
// all provisioned modules plus state-backend health. It never mutates state —
// the per-module `<module> status` commands own the provisioning→ready transition.
package status

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/cmd/internal/statusreport"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const lineWidth = 64

type command struct {
	runtime   globals.Runtime
	jsonOut   bool
	probe     bool
	out       io.Writer
	readState func() (*fabricastate.State, error)
	probeTCP  func(address string) bool
}

// New returns the "fabrica status" command. Global flags (--json) resolve at
// execution time via the source closures; --probe is local to this command.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var probe bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show health overview across all modules",
		Long: `Show an aggregate, read-only overview of every provisioned Fabrica module
(Perforce, Horde, Workstation) plus the state backend.

Reads the local state cache (.fabrica/state.json) and queries EC2 instance
state via Cloud Control. This command never modifies state.

Use --probe to additionally TCP-probe each module's readiness port. Probing
requires network reachability to the (private) instance IPs — typically a VPN
or in-VPC session — and is off by default.`,
		Example: `  # Overview of all modules and the state backend:
  fabrica status

  # Also TCP-probe each module's port (run from a VPN / in-VPC session):
  fabrica status --probe

  # Machine-readable output for scripts:
  fabrica status --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				runtime:   rt,
				jsonOut:   opts.JSONOutput,
				probe:     probe,
				out:       out,
				readState: func() (*fabricastate.State, error) { return provision.ReadState(rt) },
				probeTCP:  modstatus.DefaultProbeTCP,
			}
			return c.run(cmd.Context())
		},
	}
	cmd.Flags().BoolVar(&probe, "probe", false, "TCP-probe each module's readiness port (requires VPN/in-VPC)")
	return cmd
}

func (c command) run(ctx context.Context) error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}

	opts := statusreport.BuildOptions{
		Probe:    c.probe,
		ProbeTCP: c.probeTCP,
	}
	report := statusreport.BuildStatusReport(ctx, st, c.runtime, opts)

	if c.jsonOut {
		return c.printJSON(report)
	}
	c.printText(report)
	return nil
}

func (c command) printJSON(report statusreport.StatusReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding status: %w", err)
	}
	fmt.Fprintln(c.out, string(data))
	return nil
}

func (c command) printText(report statusreport.StatusReport) {
	fmt.Fprintln(c.out, "Fabrica status")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "%s\n", statusreport.SummaryLine(report.Summary))
	fmt.Fprintln(c.out)

	c.printBackend(report.Backend)
	fmt.Fprintln(c.out)

	if len(report.Modules) == 0 {
		c.printEmptyState(report.Backend)
		return
	}

	fmt.Fprintf(c.out, "  %-7s %-13s %-13s %s\n", "", "MODULE", "STATUS", "DETAIL")
	for _, m := range report.Modules {
		c.printModule(m)
	}
	c.printNextSteps(report.Modules)
}

func (c command) printBackend(b statusreport.StatusBackend) {
	fmt.Fprintf(c.out, "  %s State bucket:  %s\n", existsSymbol(b.BucketExists), orNotConfigured(b.Bucket))
	fmt.Fprintf(c.out, "  %s Lock table:    %s\n", existsSymbol(b.TableExists), orNotConfigured(b.Table))
}

func existsSymbol(exists string) string {
	switch exists {
	case "yes":
		return "[OK]  "
	case "no":
		return "[WARN]"
	default:
		return "[????]"
	}
}

func orNotConfigured(s string) string {
	if s == "" {
		return "(not configured)"
	}
	return s
}

func (c command) printModule(m statusreport.StatusModule) {
	detail := moduleDetail(m)
	fmt.Fprintf(c.out, "  %-7s %-13s %-13s %s\n", moduleSymbol(m.Status), m.Name, m.Status, detail)
}

func moduleSymbol(status string) string {
	switch status {
	case "ready":
		return "[OK]  "
	case "provisioning":
		return "[WARN]"
	case "stopped":
		return "[----]"
	default:
		return "[????]"
	}
}

func moduleDetail(m statusreport.StatusModule) string {
	parts := []string{fmt.Sprintf("%d %s", m.ResourceCount, plural(m.ResourceCount, "resource", "resources"))}
	if m.InstanceState != "" {
		parts = append(parts, "ec2:"+m.InstanceState)
	}
	if m.Probe != "" {
		parts = append(parts, "probe:"+m.Probe)
	}
	return strings.Join(parts, "  ")
}

func (c command) printNextSteps(modules []statusreport.StatusModule) {
	steps := statusreport.NextSteps(modules)
	if len(steps) == 0 {
		return
	}
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Next steps:")
	for _, s := range steps {
		fmt.Fprintln(c.out, s)
	}
}

func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}

// printEmptyState renders the no-modules view. When the state backend isn't
// ready it leads firmly with `fabrica setup` as the required first step;
// otherwise it lists the modules the user can provision.
func (c command) printEmptyState(backend statusreport.StatusBackend) {
	backendReady := backend.BucketExists == "yes"

	if !backendReady {
		fmt.Fprintln(c.out, "Nothing provisioned yet, and your state backend isn't set up.")
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "Start here:")
		fmt.Fprintln(c.out, "  fabrica setup                 Create the state backend (required first step)")
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "Once setup completes, you can provision modules:")
		fmt.Fprintln(c.out, "  fabrica perforce create       Provision Perforce Helix Core")
		fmt.Fprintln(c.out, "  fabrica horde create          Provision Unreal Horde")
		fmt.Fprintln(c.out, "  fabrica workstation create    Provision a cloud workstation")
		return
	}

	fmt.Fprintln(c.out, "State backend is ready, but no modules are provisioned yet.")
	fmt.Fprintln(c.out)
	fmt.Fprintln(c.out, "Next steps:")
	fmt.Fprintln(c.out, "  fabrica perforce create       Provision Perforce Helix Core")
	fmt.Fprintln(c.out, "  fabrica horde create          Provision Unreal Horde")
	fmt.Fprintln(c.out, "  fabrica workstation create    Provision a cloud workstation")
}
