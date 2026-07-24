package horde

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestCostResourcesDefaults(t *testing.T) {
	got := CostResources(config.HordeConfig{}) // empty -> defaults
	if len(got) != 2 {
		t.Fatalf("want 2 resources, got %d: %+v", len(got), got)
	}
	if got[0].TypeName != cloud.TypeAWSEC2Instance || got[0].Name != "m7i.2xlarge" {
		t.Errorf("instance: got %+v", got[0])
	}
	if got[1].TypeName != cloud.TypeAWSEC2Volume || got[1].Name != "gp3-100GiB" {
		t.Errorf("volume: got %+v", got[1])
	}
}

func TestCostResourcesOverrides(t *testing.T) {
	got := CostResources(config.HordeConfig{InstanceType: "m7i.4xlarge", VolumeSize: 200})
	if got[0].Name != "m7i.4xlarge" || got[1].Name != "gp3-200GiB" {
		t.Fatalf("overrides not applied: %+v", got)
	}
}
