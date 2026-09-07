package ddc

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
)

func TestValidateProductionTopology(t *testing.T) {
	if err := ValidateProductionTopology(config.DDCConfig{}); err != nil {
		t.Fatalf("zen default: %v", err)
	}
	if err := ValidateProductionTopology(config.DDCConfig{Backend: BackendScylla, Scylla: config.DDCScyllaConfig{Nodes: 2}}); err == nil {
		t.Fatal("expected nodes=2 error")
	}
	if err := ValidateProductionTopology(config.DDCConfig{Backend: BackendScylla, Scylla: config.DDCScyllaConfig{Nodes: 3, Replication: 5}}); err == nil {
		t.Fatal("expected RF>nodes error")
	}
	if err := ValidateProductionTopology(config.DDCConfig{Replication: config.DDCReplicationConfig{Enabled: true, Peers: []string{"", "10.0.0.2"}}}); err == nil {
		t.Fatal("expected empty peer error")
	}
}

func TestBuildTopologyPlanProduction(t *testing.T) {
	plan, err := BuildTopologyPlan(config.DDCConfig{
		Backend:     BackendScylla,
		Scylla:      config.DDCScyllaConfig{Nodes: 3, Datacenter: "us-east"},
		Replication: config.DDCReplicationConfig{Enabled: true, Peers: []string{"10.1.0.8:8080"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Production || plan.RF != 3 || plan.ScyllaNodes != 3 {
		t.Fatalf("plan = %+v", plan)
	}
	if len(plan.Peers) != 1 || !strings.Contains(plan.Note, "Replication peers") {
		t.Fatalf("peers/note = %+v", plan)
	}
}

func TestCostResourcesProductionScylla(t *testing.T) {
	one := CostResources(config.DDCConfig{Backend: BackendScylla})
	three := CostResources(config.DDCConfig{Backend: BackendScylla, Scylla: config.DDCScyllaConfig{Nodes: 3}})
	if len(three) <= len(one) {
		t.Fatalf("3-node should add cost lines: one=%d three=%d", len(one), len(three))
	}
}
