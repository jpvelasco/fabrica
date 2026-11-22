package ci

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/naming"
)

func TestBuildPipelinePlan(t *testing.T) {
	p := BuildPipelinePlan(naming.Default, config.CIConfig{})
	if p.Enabled || p.Name != "fabrica-ci-pipeline" || len(p.Stages) != 3 {
		t.Fatalf("plan = %+v", p)
	}
	if PipelineCostResources(naming.Default, config.CIConfig{}) != nil {
		t.Fatal("disabled pipeline should have no cost")
	}
	res := PipelineCostResources(naming.Default, config.CIConfig{Pipeline: true, ProjectName: "studio"})
	if len(res) != 1 || cost.Global.EstimateAll(res).Total <= 0 {
		t.Fatalf("enabled pipeline cost = %+v", res)
	}
}
