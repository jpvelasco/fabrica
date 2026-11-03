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
	"fmt"
	"strconv"
	"strings"

	"github.com/jpvelasco/fabrica/internal/ci"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/ddc"
	"github.com/jpvelasco/fabrica/internal/deploy"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/lore"
	"github.com/jpvelasco/fabrica/internal/ops"
	"github.com/jpvelasco/fabrica/internal/perforce"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/workstation"
)

// PriceCaveat is the shared one-line disclosure for cost report and forecast:
// offline estimates use a static us-east-1 Linux on-demand table, not live
// Pricing/Cost Explorer, and Spot/schedule factors are planning discounts only.
func PriceCaveat() string {
	return fmt.Sprintf("Note: estimates use a static %s Linux on-demand table (%s), not live Pricing/Cost Explorer. Spot/schedule factors are planning discounts only. Run `<module> status` to reconcile.",
		perforce.PriceTableRegion, perforce.PriceTableVintage)
}

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
		addModule(&b, m.Name, m.Status, note, reg.EstimateAll(resources))
	}
	if cfg.Ops.Enabled {
		if res := ops.CostResources(cfg.Ops); len(res) > 0 {
			addModule(&b, "ops", "enabled", "local observability hooks — CloudWatch not provisioned by Fabrica", reg.EstimateAll(res))
		}
	}
	b.PerScope["total"] = b.Total
	// Offline prices are us-east-1 snapshots. A different configured region
	// cannot be High-confidence; do not raise Low/Medium already set by estimators.
	if region := cfg.Cloud.AWS.Region; region != "" && region != perforce.PriceTableRegion && b.Confidence == cost.High {
		b.Confidence = cost.Medium
	}
	return b
}

func addModule(b *Breakdown, name, status, note string, report cost.Report) {
	b.Modules = append(b.Modules, ModuleCost{
		Name:     name,
		Status:   status,
		Report:   report,
		Subtotal: report.Total,
		Note:     note,
	})
	b.Total += report.Total
	b.PerScope[name] += report.Total
	if report.Confidence > b.Confidence {
		b.Confidence = report.Confidence
	}
}

// costInputs returns the cost resources for a module plus an optional note.
// It applies two state-aware adjustments the raw CostResources helpers cannot:
// stopped instances drop their compute line, and deploy includes the fleet line
// only when a fleet resource actually exists in state.
func costInputs(cfg *config.Config, m *state.ModuleState) ([]cost.Resource, string) {
	switch m.Name {
	case "perforce":
		res, note := applyStopped(ec2CostResources(m, perforce.CostResources(cfg.Perforce)), m.Status)
		res = append(res, perforce.BackupCostResources(cfg.Perforce.Backup)...)
		return res, note
	case "horde":
		res, note := applyStopped(ec2CostResources(m, horde.CostResources(cfg.Horde)), m.Status)
		if hasResource(m, cloud.TypeAWSAutoScalingAutoScalingGroup) {
			res = append(res, horde.AgentsCostResources(cfg.Horde.Agents)...)
		}
		return res, note
	case "lore":
		return applyStopped(ec2CostResources(m, lore.CostResources(cfg.Lore)), m.Status)
	case "workstation":
		return applyStopped(ec2CostResources(m, workstation.CostResources(cfg.Workstation)), m.Status)
	case "ci":
		res := ci.CostResources(cfg.CI)
		res = append(res, ci.PipelineCostResources(cfg.CI)...)
		return res, ""
	case "deploy":
		if !hasResource(m, deploy.TypeGameLiftFleet) {
			return nil, "setup only (no active fleet) — standing cost ~$0"
		}
		return deployCostResources(m, cfg.Deploy), deployFleetNote(m)
	case "ddc":
		// Prefer full CostResources (S3 + EC2 + EBS [+ Scylla]); do not collapse
		// via ec2CostResources which would drop the blob bucket line.
		return applyStopped(ddc.CostResources(cfg.DDC), m.Status)
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
	out := make([]cost.Resource, 0, len(cfgResources))
	for _, r := range cfgResources {
		switch r.TypeName {
		case cloud.TypeAWSEC2Instance:
			out = append(out, cost.Resource{TypeName: r.TypeName, Name: inst["instanceType"]})
		case cloud.TypeAWSEC2Volume:
			out = append(out, cost.Resource{TypeName: r.TypeName, Name: "gp3-" + inst["volumeSize"] + "GiB"})
		default:
			out = append(out, r)
		}
	}
	return out
}

// deployCostResources prices every GameLift fleet recorded in state by its own
// recorded shape (instance type + desired count): the active fleet serving
// traffic, retained (superseded) fleets still running for rollback, and
// pending (provisioning) fleets acquiring capacity. Pricing only the alias
// target undercounted billable capacity after multiple promotions; summing
// every recorded fleet keeps report, forecast, and alerts honest.
//
// Policy: a fleet prices itself when it has a recorded instanceType and a
// positive desiredInstances. Fleets without a recorded shape (legacy state)
// contribute no line; when no recorded fleet has a shape, the config-derived
// line stands in for the fleet the state knows exists.
func deployCostResources(m *state.ModuleState, cfg config.DeployConfig) []cost.Resource {
	var out []cost.Resource
	for _, r := range m.Resources {
		if r.TypeName != deploy.TypeGameLiftFleet {
			continue
		}
		if res, ok := fleetCostResource(r.Properties); ok {
			out = append(out, res)
		}
	}
	if len(out) == 0 {
		return deploy.CostResources(cfg)
	}
	return out
}

// fleetCostResource builds the cost line for one recorded fleet, or ok=false
// when the recorded shape is missing or unparseable so the caller can fall
// back to the config-derived estimate.
func fleetCostResource(props map[string]string) (cost.Resource, bool) {
	desired, err := strconv.Atoi(props["desiredInstances"])
	if props["instanceType"] == "" || err != nil || desired <= 0 {
		return cost.Resource{}, false
	}
	return cost.Resource{
		TypeName: deploy.TypeGameLiftFleet,
		Name:     deploy.FleetCostName(props["instanceType"], desired),
	}, true
}

// instanceProperties returns the Properties of the module's EC2 instance record,
// or nil if none is tracked.
func instanceProperties(m *state.ModuleState) map[string]string {
	for _, r := range m.Resources {
		if r.TypeName == cloud.TypeAWSEC2Instance {
			return r.Properties
		}
	}
	return nil
}

// deployFleetNote explains billable capacity beyond the alias target: retained
// (superseded) fleets kept running for rollback and pending (provisioning)
// fleets still acquiring capacity. Empty when only the active fleet prices.
func deployFleetNote(m *state.ModuleState) string {
	var retained, pending int
	for _, r := range m.Resources {
		if r.TypeName != deploy.TypeGameLiftFleet {
			continue
		}
		switch r.Properties["role"] {
		case "superseded":
			retained++
		case "provisioning":
			pending++
		}
	}
	var parts []string
	if retained > 0 {
		parts = append(parts, fmt.Sprintf("%d retained fleet(s) kept for rollback", retained))
	}
	if pending > 0 {
		parts = append(parts, fmt.Sprintf("%d pending fleet(s) acquiring capacity", pending))
	}
	if len(parts) == 0 {
		return ""
	}
	return "includes " + strings.Join(parts, " + ")
}

// applyStopped drops the EC2 instance (compute) line when the module is stopped;
// EBS volumes are still billed. Returns an explanatory note when it does.
func applyStopped(resources []cost.Resource, status string) ([]cost.Resource, string) {
	if status != "stopped" {
		return resources, ""
	}
	kept := resources[:0:0]
	for _, r := range resources {
		if r.TypeName == cloud.TypeAWSEC2Instance {
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
