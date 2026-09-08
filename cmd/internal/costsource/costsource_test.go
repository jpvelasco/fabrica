package costsource

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/deploy"
	"github.com/jpvelasco/fabrica/internal/lore"
	"github.com/jpvelasco/fabrica/internal/perforce"
	"github.com/jpvelasco/fabrica/internal/state"
)

func mod(name, status string, res ...state.ModuleResource) state.ModuleState {
	return state.ModuleState{Name: name, Status: status, Resources: res}
}

func TestAggregateMultiModule(t *testing.T) {
	cfg := config.Defaults()
	st := state.NewState("acct", "us-east-1")
	st.Modules = []state.ModuleState{
		mod("perforce", "ready",
			state.ModuleResource{TypeName: "AWS::EC2::Instance", Identifier: "i-1"},
			state.ModuleResource{TypeName: "AWS::EC2::Volume", Identifier: "vol-1"}),
		mod("horde", "ready",
			state.ModuleResource{TypeName: "AWS::EC2::Instance", Identifier: "i-2"},
			state.ModuleResource{TypeName: "AWS::EC2::Volume", Identifier: "vol-2"}),
	}
	b := Aggregate(cfg, st, cost.Global)
	if len(b.Modules) != 2 {
		t.Fatalf("want 2 modules, got %d", len(b.Modules))
	}
	if b.Total <= 0 {
		t.Fatalf("want positive total, got %v", b.Total)
	}
	if b.PerScope["total"] != b.Total {
		t.Fatalf("PerScope total %v != Total %v", b.PerScope["total"], b.Total)
	}
	if _, ok := b.PerScope["perforce"]; !ok {
		t.Fatalf("PerScope missing perforce")
	}
}

func TestAggregateStoppedDropsCompute(t *testing.T) {
	cfg := config.Defaults()
	st := state.NewState("acct", "us-east-1")
	st.Modules = []state.ModuleState{
		mod("workstation", "stopped",
			state.ModuleResource{TypeName: "AWS::EC2::Instance", Identifier: "i-1"},
			state.ModuleResource{TypeName: "AWS::EC2::Volume", Identifier: "vol-1"}),
	}
	cfg.Workstation.AmiID = "ami-123"
	b := Aggregate(cfg, st, cost.Global)
	ws := b.Modules[0]
	// Only the volume line should remain (compute not billed while stopped).
	for _, r := range ws.Report.Results {
		if r.Resource.TypeName == "AWS::EC2::Instance" {
			t.Fatalf("stopped module should drop the instance line: %+v", ws.Report.Results)
		}
	}
	if ws.Note == "" {
		t.Errorf("stopped module should carry a note")
	}
}

func TestAggregateDeployFleetOnlyWhenPresent(t *testing.T) {
	cfg := config.Defaults()
	// Setup-only deploy: role + alias, no fleet.
	st := state.NewState("acct", "us-east-1")
	st.Modules = []state.ModuleState{
		mod("deploy", "ready",
			state.ModuleResource{TypeName: "AWS::IAM::Role", Identifier: "r-1"},
			state.ModuleResource{TypeName: "AWS::GameLift::Alias", Identifier: "a-1"}),
	}
	b := Aggregate(cfg, st, cost.Global)
	if b.Modules[0].Subtotal != 0 {
		t.Fatalf("setup-only deploy should cost ~0, got %v", b.Modules[0].Subtotal)
	}

	// With a fleet present, the fleet cost is included.
	st.Modules[0].Resources = append(st.Modules[0].Resources,
		state.ModuleResource{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-1"})
	b = Aggregate(cfg, st, cost.Global)
	if b.Modules[0].Subtotal <= 0 {
		t.Fatalf("deploy with fleet should have positive subtotal, got %v", b.Modules[0].Subtotal)
	}
}

func TestEC2CostResourcesPrefersState(t *testing.T) {
	// State records a bigger instance than the config default; the estimate
	// must reflect the deployed shape, not the config.
	cfg := config.Defaults()
	small := ec2CostResources(&state.ModuleState{}, perforce.CostResources(cfg.Perforce))
	big := ec2CostResources(
		&state.ModuleState{Resources: []state.ModuleResource{{
			TypeName:   "AWS::EC2::Instance",
			Identifier: "i-1",
			Properties: map[string]string{"instanceType": "m5.8xlarge", "volumeSize": "1000"},
		}}},
		perforce.CostResources(cfg.Perforce),
	)
	if big[0].Name != "m5.8xlarge" {
		t.Errorf("instance name from state = %q, want m5.8xlarge", big[0].Name)
	}
	if big[1].Name != "gp3-1000GiB" {
		t.Errorf("volume name from state = %q, want gp3-1000GiB", big[1].Name)
	}
	if small[0].Name == big[0].Name {
		t.Errorf("expected state to override config default (%q)", small[0].Name)
	}
}

func TestEC2CostResourcesKeepsNonComputeLines(t *testing.T) {
	cfg := config.Defaults()
	cfg.Lore.StoreBackend = "s3"
	cfg.Lore.StoreBucket = "lore-store"
	cfg.Lore.AmiID = "ami-lore"
	got := ec2CostResources(
		&state.ModuleState{Resources: []state.ModuleResource{{
			TypeName:   cloud.TypeAWSEC2Instance,
			Identifier: "i-1",
			Properties: map[string]string{"instanceType": "m5.2xlarge", "volumeSize": "800"},
		}}},
		lore.CostResources(cfg.Lore),
	)
	if !containsType(got, cloud.TypeAWSS3Bucket) || !containsType(got, cloud.TypeAWSDynamoDBTable) {
		t.Fatalf("lore storage lines dropped: %+v", got)
	}
	if got[0].Name != "m5.2xlarge" {
		t.Errorf("instance overlay = %q, want m5.2xlarge", got[0].Name)
	}
}

func TestEC2CostResourcesFallsBackWhenPropertiesMissing(t *testing.T) {
	// Old state (no Properties) must fall back to the config-derived shape.
	cfg := config.Defaults()
	cfgRes := perforce.CostResources(cfg.Perforce)
	got := ec2CostResources(
		&state.ModuleState{Resources: []state.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-1"},
		}},
		cfgRes,
	)
	if got[0].Name != cfgRes[0].Name {
		t.Errorf("fallback instance = %q, want config %q", got[0].Name, cfgRes[0].Name)
	}
}

func TestAggregateDeployFleetPrefersState(t *testing.T) {
	cfg := config.Defaults()
	st := state.NewState("acct", "us-east-1")
	st.Modules = []state.ModuleState{
		mod("deploy", "ready",
			state.ModuleResource{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-1",
				Properties: map[string]string{"instanceType": "c5.large", "desiredInstances": "3"}}),
	}
	b := Aggregate(cfg, st, cost.Global)
	// 3 instances must cost more than the config default (1 instance).
	cfgOnly := deploy.CostResources(cfg.Deploy)
	base := cost.Global.EstimateAll(cfgOnly).Total
	if b.Modules[0].Subtotal <= base {
		t.Errorf("state fleet (3 desired) subtotal %v should exceed config-default %v", b.Modules[0].Subtotal, base)
	}
}

func TestAggregateDeployPricesActiveFleetNotSuperseded(t *testing.T) {
	// After a second promote, the superseded fleet comes first in resource
	// order and the active fleet later. Cost must reflect the ACTIVE fleet (the
	// live alias target), not the first/superseded one.
	cfg := config.Defaults()
	st := state.NewState("acct", "us-east-1")
	st.Modules = []state.ModuleState{
		mod("deploy", "ready",
			state.ModuleResource{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-old",
				Properties: map[string]string{"role": "superseded", "instanceType": "c5.large", "desiredInstances": "1"}},
			state.ModuleResource{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-new",
				Properties: map[string]string{"role": "active", "instanceType": "c5.large", "desiredInstances": "5"}}),
	}
	got := deployCostResources(&st.Modules[0], cfg.Deploy)
	want := deploy.FleetCostName("c5.large", 5)
	if got[0].Name != want {
		t.Errorf("priced fleet = %q, want the active fleet %q (not the superseded c5.largex1)", got[0].Name, want)
	}
}

func TestPropertyLookupsReturnNilWhenAbsent(t *testing.T) {
	empty := &state.ModuleState{}
	if instanceProperties(empty) != nil {
		t.Error("instanceProperties should be nil when no instance is tracked")
	}
	if fleetProperties(empty) != nil {
		t.Error("fleetProperties should be nil when no fleet is tracked")
	}
	// deployCostResources with a fleet that has no Properties falls back to config.
	cfg := config.Defaults()
	m := &state.ModuleState{Resources: []state.ModuleResource{
		{TypeName: deploy.TypeGameLiftFleet, Identifier: "fleet-1"},
	}}
	got := deployCostResources(m, cfg.Deploy)
	want := deploy.CostResources(cfg.Deploy)
	if len(got) != len(want) || got[0].Name != want[0].Name {
		t.Errorf("fallback = %+v, want %+v", got, want)
	}
}

func TestMapBudgets(t *testing.T) {
	in := []config.BudgetThreshold{
		{Scope: "total", Monthly: 500, WarnPct: 80},
		{Scope: "perforce", Monthly: 150, WarnPct: 0},
	}
	got := MapBudgets(in)
	if len(got) != 2 {
		t.Fatalf("want 2 budgets, got %d", len(got))
	}
	if got[0].Scope != "total" || got[0].Monthly != 500 || got[0].WarnPct != 80 {
		t.Errorf("budget[0] = %+v", got[0])
	}
	if got[1].Scope != "perforce" || got[1].Monthly != 150 {
		t.Errorf("budget[1] = %+v", got[1])
	}
	if len(MapBudgets(nil)) != 0 {
		t.Error("MapBudgets(nil) should be empty")
	}
}

func TestAggregateLoreKeepsStorageWhenStateHasInstanceProps(t *testing.T) {
	cfg := config.Defaults()
	cfg.Lore.StoreBackend = "s3"
	cfg.Lore.StoreBucket = "lore-store"
	cfg.Lore.AmiID = "ami-lore"
	st := state.NewState("acct", "us-east-1")
	st.Modules = []state.ModuleState{
		mod("lore", "ready",
			state.ModuleResource{
				TypeName:   cloud.TypeAWSEC2Instance,
				Identifier: "i-1",
				Properties: map[string]string{"instanceType": "m5.2xlarge", "volumeSize": "800"},
			}),
	}
	b := Aggregate(cfg, st, cost.Global)
	if !reportHasType(b.Modules[0].Report, cloud.TypeAWSS3Bucket) || !reportHasType(b.Modules[0].Report, cloud.TypeAWSDynamoDBTable) {
		t.Fatalf("lore storage dropped from aggregate: %+v", b.Modules[0].Report.Results)
	}
}

func TestAggregateHordeIncludesAgentsWhenASGPresent(t *testing.T) {
	cfg := config.Defaults()
	cfg.Horde.AmiID = "ami-h"
	cfg.Horde.Agents.AmiID = "ami-a"
	cfg.Horde.Agents.DesiredCapacity = 3
	st := state.NewState("acct", "us-east-1")
	st.Modules = []state.ModuleState{
		mod("horde", "ready",
			state.ModuleResource{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-1"},
			state.ModuleResource{TypeName: cloud.TypeAWSAutoScalingAutoScalingGroup, Identifier: "asg-1"}),
	}
	withAgents := Aggregate(cfg, st, cost.Global)
	st.Modules[0].Resources = st.Modules[0].Resources[:1]
	withoutAgents := Aggregate(cfg, st, cost.Global)
	if withAgents.Modules[0].Subtotal <= withoutAgents.Modules[0].Subtotal {
		t.Fatalf("agents should add cost: with=%v without=%v", withAgents.Modules[0].Subtotal, withoutAgents.Modules[0].Subtotal)
	}
	if !reportHasType(withAgents.Modules[0].Report, cloud.TypeAWSAutoScalingAutoScalingGroup) {
		t.Fatal("expected ASG cost line when agents exist in state")
	}
}

func reportHasType(r cost.Report, typeName string) bool {
	res := make([]cost.Resource, len(r.Results))
	for i, item := range r.Results {
		res[i] = item.Resource
	}
	return containsType(res, typeName)
}

func containsType(res []cost.Resource, typeName string) bool {
	for _, r := range res {
		if r.TypeName == typeName {
			return true
		}
	}
	return false
}

func TestAggregatePerforceIncludesScheduledBackup(t *testing.T) {
	cfg := config.Defaults()
	cfg.Perforce.Backup.Schedule = "0 3 * * *"
	cfg.Perforce.Backup.Retain = 5
	st := state.NewState("acct", "us-east-1")
	st.Modules = []state.ModuleState{mod("perforce", "ready",
		state.ModuleResource{TypeName: "AWS::EC2::Instance", Identifier: "i-1"})}
	withSched := Aggregate(cfg, st, cost.Global)
	without := Aggregate(config.Defaults(), st, cost.Global)
	if withSched.Modules[0].Subtotal <= without.Modules[0].Subtotal {
		t.Fatalf("schedule should add cost: with=%v without=%v", withSched.Modules[0].Subtotal, without.Modules[0].Subtotal)
	}
}

func TestAggregateOpsWhenEnabled(t *testing.T) {
	cfg := config.Defaults()
	cfg.Ops.Enabled = true
	cfg.Ops.Modules = []string{"horde"}
	st := state.NewState("acct", "us-east-1")
	off := Aggregate(config.Defaults(), st, cost.Global)
	on := Aggregate(cfg, st, cost.Global)
	if len(on.Modules) != 1 || on.Modules[0].Name != "ops" {
		t.Fatalf("ops module missing: %+v", on.Modules)
	}
	if on.Total <= off.Total {
		t.Fatalf("enabled ops should add cost: on=%v off=%v", on.Total, off.Total)
	}
}

func TestPriceCaveatDisclosesStaticTable(t *testing.T) {
	got := PriceCaveat()
	for _, want := range []string{
		perforce.PriceTableRegion,
		perforce.PriceTableVintage,
		"on-demand",
		"Pricing",
		"planning discounts",
		"<module> status",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("PriceCaveat() missing %q: %s", want, got)
		}
	}
}

func TestAggregateOffTableRegionLowersHighConfidence(t *testing.T) {
	st := state.NewState("acct", "us-west-2")
	st.Modules = []state.ModuleState{
		mod("perforce", "ready",
			state.ModuleResource{TypeName: "AWS::EC2::Instance", Identifier: "i-1"},
			state.ModuleResource{TypeName: "AWS::EC2::Volume", Identifier: "vol-1"}),
	}

	onTable := config.Defaults()
	if onTable.Cloud.AWS.Region != perforce.PriceTableRegion {
		t.Fatalf("Defaults region = %q, want %s", onTable.Cloud.AWS.Region, perforce.PriceTableRegion)
	}
	on := Aggregate(onTable, st, cost.Global)
	if on.Confidence != cost.High {
		t.Fatalf("us-east-1 confidence = %v, want High", on.Confidence)
	}

	offTable := config.Defaults()
	offTable.Cloud.AWS.Region = "us-west-2"
	off := Aggregate(offTable, st, cost.Global)
	if off.Confidence != cost.Medium {
		t.Fatalf("us-west-2 confidence = %v, want Medium", off.Confidence)
	}

	// Do not raise an already-Low estimate just because the region mismatches.
	lowCfg := config.Defaults()
	lowCfg.Cloud.AWS.Region = "us-west-2"
	lowSt := state.NewState("acct", "us-west-2")
	lowSt.Modules = []state.ModuleState{mod("ci", "ready")}
	low := Aggregate(lowCfg, lowSt, cost.Global)
	if low.Confidence != cost.Low {
		t.Fatalf("ci off-table should stay Low, got %v", low.Confidence)
	}
}

func TestAggregateUnknownModule(t *testing.T) {
	cfg := config.Defaults()
	st := state.NewState("acct", "us-east-1")
	st.Modules = []state.ModuleState{mod("mystery", "ready")}
	b := Aggregate(cfg, st, cost.Global)
	if len(b.Modules) != 1 || b.Modules[0].Subtotal != 0 {
		t.Fatalf("unknown module should contribute 0: %+v", b.Modules)
	}
	if b.Modules[0].Note == "" {
		t.Errorf("unknown module should carry a 'no estimator wired' note")
	}
}
