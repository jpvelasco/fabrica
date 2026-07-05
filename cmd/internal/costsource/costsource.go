// Package costsource is the shared engine that turns provisioned state plus the
// current config into an estimated cost breakdown. It is the single owner of
// module enumeration for the cost commands (report, forecast, alerts), the same
// way modstatus owns status enumeration and teardown owns delete ordering.
//
// It is fully offline: it reads local state (which modules exist) and derives
// cost inputs from config (what those modules are configured as). No AWS SDK,
// no live provider.
package costsource

import (
	"strconv"

	"github.com/jpvelasco/fabrica/internal/ci"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/deploy"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/perforce"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/workstation"
)

// ModuleCost is the estimated cost for one provisioned module.
type ModuleCost struct {
	Name     string
	Status   string
	Report   cost.Report
	Subtotal float64
	Note     string
}

// Breakdown is the full cost picture across all provisioned modules.
type Breakdown struct {
	Modules    []ModuleCost
	Total      float64
	Confidence cost.ConfidenceLevel
	PerScope   map[string]float64 // module subtotals + "total"
}

// Aggregate builds the cost breakdown for the modules present in state, using
// cost inputs derived from cfg. The switch on module name is the only place
// that enumerates modules.
func Aggregate(cfg *config.Config, st *state.State, reg *cost.Registry) Breakdown {
	b := Breakdown{
		Confidence: cost.High,
		PerScope:   make(map[string]float64),
	}
	for i := range st.Modules {
		m := &st.Modules[i]
		resources, note := costInputs(cfg, m)
		report := reg.EstimateAll(resources)
		mc := ModuleCost{
			Name:     m.Name,
			Status:   m.Status,
			Report:   report,
			Subtotal: report.Total,
			Note:     note,
		}
		b.Modules = append(b.Modules, mc)
		b.Total += report.Total
		b.PerScope[m.Name] += report.Total
		if report.Confidence > b.Confidence {
			b.Confidence = report.Confidence
		}
	}
	b.PerScope["total"] = b.Total
	return b
}

// costInputs returns the cost resources for a module plus an optional note.
// It applies two state-aware adjustments the raw CostResources helpers cannot:
// stopped instances drop their compute line, and deploy includes the fleet line
// only when a fleet resource actually exists in state.
func costInputs(cfg *config.Config, m *state.ModuleState) ([]cost.Resource, string) {
	switch m.Name {
	case "perforce":
		return applyStopped(ec2CostResources(m, perforce.CostResources(cfg.Perforce)), m.Status)
	case "horde":
		return applyStopped(ec2CostResources(m, horde.CostResources(cfg.Horde)), m.Status)
	case "workstation":
		return applyStopped(ec2CostResources(m, workstation.CostResources(cfg.Workstation)), m.Status)
	case "ci":
		return ci.CostResources(cfg.CI), ""
	case "deploy":
		if !hasResource(m, deploy.TypeGameLiftFleet) {
			return nil, "setup only (no active fleet) — standing cost ~$0"
		}
		return deployCostResources(m, cfg.Deploy), ""
	default:
		return nil, "no estimator wired for this module"
	}
}

// ec2CostResources prefers the instance type and volume size recorded in state
// at create time (the actual deployed shape) over the config-derived fallback.
// Older state written before the Properties backfill has no such keys, so it
// falls back to cfgResources unchanged — the config still reflects the intent.
func ec2CostResources(m *state.ModuleState, cfgResources []cost.Resource) []cost.Resource {
	inst := instanceProperties(m)
	if inst["instanceType"] == "" || inst["volumeSize"] == "" {
		return cfgResources
	}
	return []cost.Resource{
		{TypeName: "AWS::EC2::Instance", Name: inst["instanceType"]},
		{TypeName: "AWS::EC2::Volume", Name: "gp3-" + inst["volumeSize"] + "GiB"},
	}
}

// deployCostResources prefers the fleet shape recorded in state (instance type +
// desired count) over the config fallback, mirroring ec2CostResources.
func deployCostResources(m *state.ModuleState, cfg config.DeployConfig) []cost.Resource {
	fleet := fleetProperties(m)
	desired, err := strconv.Atoi(fleet["desiredInstances"])
	if fleet["instanceType"] == "" || err != nil || desired <= 0 {
		return deploy.CostResources(cfg)
	}
	return []cost.Resource{
		{TypeName: deploy.TypeGameLiftFleet, Name: deploy.FleetCostName(fleet["instanceType"], desired)},
	}
}

// instanceProperties returns the Properties of the module's EC2 instance record,
// or nil if none is tracked.
func instanceProperties(m *state.ModuleState) map[string]string {
	for _, r := range m.Resources {
		if r.TypeName == "AWS::EC2::Instance" {
			return r.Properties
		}
	}
	return nil
}

// fleetProperties returns the Properties of the module's GameLift fleet record,
// or nil if none is tracked.
func fleetProperties(m *state.ModuleState) map[string]string {
	for _, r := range m.Resources {
		if r.TypeName == deploy.TypeGameLiftFleet {
			return r.Properties
		}
	}
	return nil
}

// applyStopped drops the EC2 instance (compute) line when the module is stopped;
// EBS volumes are still billed. Returns an explanatory note when it does.
func applyStopped(resources []cost.Resource, status string) ([]cost.Resource, string) {
	if status != "stopped" {
		return resources, ""
	}
	kept := resources[:0:0]
	for _, r := range resources {
		if r.TypeName == "AWS::EC2::Instance" {
			continue
		}
		kept = append(kept, r)
	}
	return kept, "stopped — compute not billed (EBS still billed)"
}

func hasResource(m *state.ModuleState, typeName string) bool {
	for _, r := range m.Resources {
		if r.TypeName == typeName {
			return true
		}
	}
	return false
}

// MapBudgets converts config budget thresholds into cost budget thresholds,
// keeping internal/cost free of the config dependency.
func MapBudgets(in []config.BudgetThreshold) []cost.BudgetThreshold {
	out := make([]cost.BudgetThreshold, 0, len(in))
	for _, b := range in {
		out = append(out, cost.BudgetThreshold{Scope: b.Scope, Monthly: b.Monthly, WarnPct: b.WarnPct})
	}
	return out
}
