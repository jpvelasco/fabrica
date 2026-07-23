// Package status implements `fabrica status`: a read-only aggregate overview of
// all provisioned modules plus state-backend health. It never mutates state —
// the per-module `<module> status` commands own the provisioning→ready transition.
package status

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

const lineWidth = 64

// probePorts maps module name → readiness TCP port (used only with --probe).
var probePorts = map[string]int{
	"perforce":    1666,
	"horde":       5000,
	"lore":        41339,
	"workstation": 8443,
}

// StatusReport is the JSON view of the aggregate status.
type StatusReport struct {
	Backend StatusBackend  `json:"backend"`
	Modules []StatusModule `json:"modules"`
	Summary StatusSummary  `json:"summary"`
}

// StatusBackend reports state-backend health. Existence fields are "yes", "no",
// or "unknown" (provider unavailable or check failed).
type StatusBackend struct {
	Bucket       string `json:"bucket,omitempty"`
	BucketExists string `json:"bucketExists"`
	Table        string `json:"table,omitempty"`
	TableExists  string `json:"tableExists"`
}

// StatusModule is the per-module overview line.
type StatusModule struct {
	Name          string `json:"name"`
	Status        string `json:"status"`
	Version       string `json:"version,omitempty"`
	ResourceCount int    `json:"resourceCount"`
	InstanceID    string `json:"instanceId,omitempty"`
	SGID          string `json:"sgId,omitempty"`
	InstanceState string `json:"instanceState,omitempty"`
	Probe         string `json:"probe,omitempty"`
}

// StatusSummary aggregates counts across all modules.
type StatusSummary struct {
	ModuleCount   int `json:"moduleCount"`
	ResourceCount int `json:"resourceCount"`
	Healthy       int `json:"healthy"`
	Provisioning  int `json:"provisioning"`
}

type command struct {
	runtime     globals.Runtime
	jsonOut     bool
	probe       bool
	out         io.Writer
	readState   func() (*fabricastate.State, error)
	getResource func(ctx context.Context, r *cloud.Resource) error
	backend     cloud.StateBackendChecker
	probeTCP    func(address string) bool
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
				readState: func() (*fabricastate.State, error) { return readState(rt) },
				probeTCP:  defaultProbeTCP,
			}
			if rt.Provider != nil {
				c.getResource = rt.Provider.Resources().Get
				if b, ok := rt.Provider.(cloud.StateBackendChecker); ok {
					c.backend = b
				}
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

	backend := c.checkBackend(ctx)
	modules := c.buildModules(ctx, st)

	report := StatusReport{
		Backend: backend,
		Modules: modules,
		Summary: summarize(modules, st.ModuleCount()),
	}

	if c.jsonOut {
		return c.printJSON(report)
	}
	c.printText(report)
	return nil
}

func (c command) checkBackend(ctx context.Context) StatusBackend {
	b := StatusBackend{BucketExists: "unknown", TableExists: "unknown"}
	if c.runtime.Config == nil {
		return b
	}
	b.Bucket = c.runtime.Config.State.Bucket
	b.Table = c.runtime.Config.State.Table
	if c.backend == nil {
		return b
	}
	if b.Bucket != "" {
		b.BucketExists = yesNo(c.backend.StateBucketExists(ctx, b.Bucket))
	}
	if b.Table != "" {
		b.TableExists = yesNo(c.backend.StateLockTableExists(ctx, b.Table))
	}
	return b
}

func yesNo(ok bool, err error) string {
	if err != nil {
		return "unknown"
	}
	if ok {
		return "yes"
	}
	return "no"
}

func (c command) buildModules(ctx context.Context, st *fabricastate.State) []StatusModule {
	out := make([]StatusModule, 0, len(st.Modules))
	for i := range st.Modules {
		m := &st.Modules[i]
		sm := StatusModule{
			Name:          m.Name,
			Status:        m.Status,
			Version:       m.Version,
			ResourceCount: len(m.Resources),
		}
		if sg, ok := stateutil.ResourceByType(m, "AWS::EC2::SecurityGroup"); ok {
			sm.SGID = sg.Identifier
		}
		if inst, ok := stateutil.ResourceByType(m, "AWS::EC2::Instance"); ok {
			sm.InstanceID = inst.Identifier
			ecState, privateIP := c.liveInstance(ctx, inst.Identifier)
			sm.InstanceState = ecState
			if c.probe {
				// Degrade gracefully: when the private IP is unknown (off-VPC,
				// Cloud Control unavailable, or no port mapping) say so rather
				// than silently omitting the probe result.
				if privateIP == "" {
					sm.Probe = "skipped (no reachable address)"
				} else {
					sm.Probe = c.probeModule(m.Name, privateIP)
				}
			}
		}
		out = append(out, sm)
	}
	return out
}

func (c command) liveInstance(ctx context.Context, instanceID string) (state, privateIP string) {
	if c.getResource == nil || instanceID == "" {
		return "", ""
	}
	r := &cloud.Resource{TypeName: "AWS::EC2::Instance", Identifier: instanceID}
	if err := c.getResource(ctx, r); err != nil {
		return "", ""
	}
	return parseInstanceState(r.ActualState)
}

func parseInstanceState(raw []byte) (state, privateIP string) {
	if len(raw) == 0 {
		return "", ""
	}
	var actual struct {
		State struct {
			Name string `json:"Name"`
		} `json:"State"`
		PrivateIPAddress string `json:"PrivateIpAddress"`
	}
	if err := json.Unmarshal(raw, &actual); err != nil {
		return "", ""
	}
	return actual.State.Name, actual.PrivateIPAddress
}

// printEmptyState renders the no-modules view. When the state backend isn't
// ready it leads firmly with `fabrica setup` as the required first step;
// otherwise it lists the modules the user can provision.
func (c command) printEmptyState(backend StatusBackend) {
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

func (c command) printJSON(report StatusReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding status: %w", err)
	}
	fmt.Fprintln(c.out, string(data))
	return nil
}

func (c command) printText(report StatusReport) {
	fmt.Fprintln(c.out, "Fabrica status")
	fmt.Fprintln(c.out, strings.Repeat("-", lineWidth))
	fmt.Fprintf(c.out, "%s\n", summaryLine(report.Summary))
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

// summaryLine is the one-line health overview, e.g.
// "3 modules • 2 healthy • 1 provisioning • 7 resources".
func summaryLine(s StatusSummary) string {
	if s.ModuleCount == 0 {
		return "No modules provisioned"
	}
	parts := []string{fmt.Sprintf("%d %s", s.ModuleCount, plural(s.ModuleCount, "module", "modules"))}
	if s.Healthy > 0 {
		parts = append(parts, fmt.Sprintf("%d healthy", s.Healthy))
	}
	if s.Provisioning > 0 {
		parts = append(parts, fmt.Sprintf("%d provisioning", s.Provisioning))
	}
	parts = append(parts, fmt.Sprintf("%d %s", s.ResourceCount, plural(s.ResourceCount, "resource", "resources")))
	return strings.Join(parts, " • ")
}

func summarize(modules []StatusModule, resourceCount int) StatusSummary {
	s := StatusSummary{ModuleCount: len(modules), ResourceCount: resourceCount}
	for _, m := range modules {
		switch m.Status {
		case "ready":
			s.Healthy++
		case "provisioning":
			s.Provisioning++
		}
	}
	return s
}

func (c command) printBackend(b StatusBackend) {
	fmt.Fprintf(c.out, "  %s State bucket:  %s\n", existsSymbol(b.BucketExists), orNotConfigured(b.Bucket))
	fmt.Fprintf(c.out, "  %s Lock table:    %s\n", existsSymbol(b.TableExists), orNotConfigured(b.Table))
}

// existsSymbol maps a yes/no/unknown existence result to a status indicator.
// "no" is a warning (run setup), not a failure — the backend is simply absent.
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

func (c command) printModule(m StatusModule) {
	detail := moduleDetail(m)
	fmt.Fprintf(c.out, "  %-7s %-13s %-13s %s\n", moduleSymbol(m.Status), m.Name, m.Status, detail)
}

// moduleSymbol maps a module status to a status indicator.
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

// moduleDetail builds the right-hand detail column (resource count + live EC2
// state + probe result when available).
func moduleDetail(m StatusModule) string {
	parts := []string{fmt.Sprintf("%d %s", m.ResourceCount, plural(m.ResourceCount, "resource", "resources"))}
	if m.InstanceState != "" {
		parts = append(parts, "ec2:"+m.InstanceState)
	}
	if m.Probe != "" {
		parts = append(parts, "probe:"+m.Probe)
	}
	return strings.Join(parts, "  ")
}

func (c command) printNextSteps(modules []StatusModule) {
	var steps []string
	for _, m := range modules {
		if m.Status == "provisioning" {
			steps = append(steps, fmt.Sprintf("  fabrica %s status     Watch %s finish provisioning", m.Name, m.Name))
		}
	}
	sort.Strings(steps)
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

func (c command) probeModule(module, privateIP string) string {
	port, ok := probePorts[module]
	if !ok || c.probeTCP == nil {
		return "skipped (no known port)"
	}
	if c.probeTCP(fmt.Sprintf("%s:%d", privateIP, port)) {
		return "responding"
	}
	return "unreachable"
}

// defaultProbeTCP is the real readiness probe, shared with the per-module
// status commands.
func defaultProbeTCP(address string) bool {
	return modstatus.DefaultProbeTCP(address)
}

func readState(rt globals.Runtime) (*fabricastate.State, error) {
	return provision.ReadState(rt)
}
