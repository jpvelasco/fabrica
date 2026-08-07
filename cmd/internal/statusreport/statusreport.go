// Package statusreport provides the shared status report builder used by both
// the CLI status command and the MCP fabrica_status tool. It is importable from
// cmd/mcp without introducing a dependency on cmd/status.
package statusreport

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
)

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

// BuildOptions configures optional behavior for BuildStatusReport.
type BuildOptions struct {
	// Probe enables TCP-probing of module readiness ports.
	Probe bool
	// ProbeTCP is the TCP probe function. Nil disables probing even if Probe is true.
	ProbeTCP func(address string) bool
}

// BuildStatusReport builds the aggregate status report from state and live cloud data.
// It never mutates state. The provider may be nil (backend fields will be "unknown").
func BuildStatusReport(ctx context.Context, st *fabricastate.State, rt globals.Runtime, opts BuildOptions) StatusReport {
	backend := checkBackend(ctx, rt)
	modules := buildModules(ctx, st, rt, opts)

	return StatusReport{
		Backend: backend,
		Modules: modules,
		Summary: summarize(modules, st.ModuleCount()),
	}
}

// NextSteps returns actionable next-step commands for modules in provisioning state.
func NextSteps(modules []StatusModule) []string {
	var steps []string
	for _, m := range modules {
		if m.Status == "provisioning" {
			steps = append(steps, "  fabrica "+m.Name+" status     Watch "+m.Name+" finish provisioning")
		}
	}
	sort.Strings(steps)
	return steps
}

// SummaryLine returns the one-line health overview, e.g.
// "3 modules • 2 healthy • 1 provisioning • 7 resources".
func SummaryLine(s StatusSummary) string {
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

func checkBackend(ctx context.Context, rt globals.Runtime) StatusBackend {
	b := StatusBackend{BucketExists: "unknown", TableExists: "unknown"}
	if rt.Config == nil {
		return b
	}
	b.Bucket = rt.Config.State.Bucket
	b.Table = rt.Config.State.Table
	backend, _ := rt.Provider.(cloud.StateBackendChecker)
	if backend == nil {
		return b
	}
	if b.Bucket != "" {
		b.BucketExists = yesNo(backend.StateBucketExists(ctx, b.Bucket))
	}
	if b.Table != "" {
		b.TableExists = yesNo(backend.StateLockTableExists(ctx, b.Table))
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

// probePorts maps module name → readiness TCP port (used only with --probe).
var probePorts = map[string]int{
	"perforce":    1666,
	"horde":       5000,
	"lore":        41339,
	"workstation": 8443,
}

func buildModules(ctx context.Context, st *fabricastate.State, rt globals.Runtime, opts BuildOptions) []StatusModule {
	out := make([]StatusModule, 0, len(st.Modules))
	getResource := func(ctx context.Context, r *cloud.Resource) error {
		if rt.Provider == nil {
			return cloud.ErrResourceNotFound
		}
		return rt.Provider.Resources().Get(ctx, r)
	}

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
			ecState, privateIP := liveInstance(ctx, inst.Identifier, getResource)
			sm.InstanceState = ecState
			if opts.Probe && opts.ProbeTCP != nil {
				if privateIP == "" {
					sm.Probe = "skipped (no reachable address)"
				} else {
					sm.Probe = probeModule(m.Name, privateIP, opts.ProbeTCP)
				}
			}
		}
		out = append(out, sm)
	}
	return out
}

func liveInstance(ctx context.Context, instanceID string, getResource func(ctx context.Context, r *cloud.Resource) error) (state, privateIP string) {
	if getResource == nil || instanceID == "" {
		return "", ""
	}
	r := &cloud.Resource{TypeName: "AWS::EC2::Instance", Identifier: instanceID}
	if err := getResource(ctx, r); err != nil {
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

func probeModule(module, privateIP string, probeTCP func(address string) bool) string {
	port, ok := probePorts[module]
	if !ok {
		return "skipped (no known port)"
	}
	addr := privateIP + ":" + strconv.Itoa(port)
	if probeTCP(addr) {
		return "responding"
	}
	return "unreachable"
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

func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}
