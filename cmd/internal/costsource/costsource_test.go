package costsource

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
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
