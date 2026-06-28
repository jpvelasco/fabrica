package deploy

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/cost"
)

func TestFleetEstimator(t *testing.T) {
	m, err := cost.Global.Estimate(TypeGameLiftFleet, cost.Resource{TypeName: TypeGameLiftFleet, Name: fleetCostName("c5.large", 2)})
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	// c5.large ~ $0.085/hr * 730 * 2 instances ~= $124.10
	if m.Amount < 100 || m.Amount > 150 {
		t.Errorf("fleet monthly = %.2f, expected ~124", m.Amount)
	}
}

func TestFleetEstimatorUnknownType(t *testing.T) {
	_, err := cost.Global.Estimate(TypeGameLiftFleet, cost.Resource{TypeName: TypeGameLiftFleet, Name: "wat.huge x9"})
	if err == nil {
		t.Error("expected error for unparseable fleet cost name")
	}
}

func TestBuildAndAliasFree(t *testing.T) {
	for _, tn := range []string{TypeGameLiftBuild, TypeGameLiftAlias} {
		m, err := cost.Global.Estimate(tn, cost.Resource{TypeName: tn, Name: "x"})
		if err != nil {
			t.Fatalf("%s: %v", tn, err)
		}
		if m.Amount != 0 {
			t.Errorf("%s should be free, got %.2f", tn, m.Amount)
		}
	}
}
