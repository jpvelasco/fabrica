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
		Summary: StatusSummary{ModuleCount: len(modules), ResourceCount: st.ModuleCount()},
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
			if c.probe && privateIP != "" {
				sm.Probe = c.probeModule(m.Name, privateIP)
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
	c.printBackend(report.Backend)
	fmt.Fprintln(c.out)

	if len(report.Modules) == 0 {
		fmt.Fprintln(c.out, "No modules provisioned yet.")
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "Next steps:")
		fmt.Fprintln(c.out, "  fabrica setup                 Provision the state backend")
		fmt.Fprintln(c.out, "  fabrica perforce create       Provision Perforce Helix Core")
		fmt.Fprintln(c.out, "  fabrica horde create          Provision Unreal Horde")
		fmt.Fprintln(c.out, "  fabrica workstation create    Provision a cloud workstation")
		return
	}

	for _, m := range report.Modules {
		c.printModule(m)
	}
	fmt.Fprintln(c.out)
	fmt.Fprintf(c.out, "%d module(s) · %d resource(s)\n", report.Summary.ModuleCount, report.Summary.ResourceCount)
	c.printNextSteps(report.Modules)
}

func (c command) printBackend(b StatusBackend) {
	fmt.Fprintf(c.out, "  State bucket:  %s [%s]\n", orNotConfigured(b.Bucket), b.BucketExists)
	fmt.Fprintf(c.out, "  Lock table:    %s [%s]\n", orNotConfigured(b.Table), b.TableExists)
}

func orNotConfigured(s string) string {
	if s == "" {
		return "(not configured)"
	}
	return s
}

func (c command) printModule(m StatusModule) {
	line := fmt.Sprintf("  %-12s %-12s %d resource(s)", m.Name, m.Status, m.ResourceCount)
	if m.InstanceState != "" {
		line += fmt.Sprintf("  ec2:%s", m.InstanceState)
	}
	if m.Probe != "" {
		line += fmt.Sprintf("  probe:%s", m.Probe)
	}
	fmt.Fprintln(c.out, line)
}

func (c command) printNextSteps(modules []StatusModule) {
	var steps []string
	for _, m := range modules {
		if m.Status == "provisioning" {
			steps = append(steps, fmt.Sprintf("  fabrica %s status     Watch %s become ready", m.Name, m.Name))
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

func (c command) probeModule(module, privateIP string) string {
	port, ok := probePorts[module]
	if !ok || c.probeTCP == nil {
		return ""
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
	account, region := "", ""
	if rt.Config != nil {
		account = rt.Config.Cloud.AWS.AccountID
		region = rt.Config.Cloud.AWS.Region
	}
	return fabricastate.ReadStateOrNew(account, region)
}
