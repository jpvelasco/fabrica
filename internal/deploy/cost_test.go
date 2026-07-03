package deploy

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
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

func TestCostResourcesDefaults(t *testing.T) {
	got := CostResources(config.DeployConfig{})
	if len(got) != 1 {
		t.Fatalf("want 1 resource (fleet only), got %d: %+v", len(got), got)
	}
	if got[0].TypeName != TypeGameLiftFleet {
		t.Errorf("TypeName: got %s, want %s", got[0].TypeName, TypeGameLiftFleet)
	}
	expectedName := fleetCostName(defaultInstanceType, defaultDesiredInstances)
	if got[0].Name != expectedName {
		t.Errorf("Name: got %s, want %s", got[0].Name, expectedName)
	}
}

func TestCostResourcesOverrides(t *testing.T) {
	got := CostResources(config.DeployConfig{InstanceType: "c5.xlarge", DesiredInstances: 3})
	if len(got) != 1 {
		t.Fatalf("want 1 resource, got %d", len(got))
	}
	expectedName := fleetCostName("c5.xlarge", 3)
	if got[0].Name != expectedName {
		t.Errorf("overrides not applied: got %s, want %s", got[0].Name, expectedName)
	}
}
