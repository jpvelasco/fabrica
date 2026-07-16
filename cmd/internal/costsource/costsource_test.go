package costsource

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/deploy"
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
