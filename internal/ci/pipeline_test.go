package ci

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

func TestBuildPipelinePlan(t *testing.T) {
	p := BuildPipelinePlan(config.CIConfig{})
	if p.Enabled || p.Name != "fabrica-ci-pipeline" || len(p.Stages) != 3 {
		t.Fatalf("plan = %+v", p)
	}
	if PipelineCostResources(config.CIConfig{}) != nil {
		t.Fatal("disabled pipeline should have no cost")
	}
	res := PipelineCostResources(config.CIConfig{Pipeline: true, ProjectName: "studio"})
	if len(res) != 1 || cost.Global.EstimateAll(res).Total <= 0 {
		t.Fatalf("enabled pipeline cost = %+v", res)
	}
}
