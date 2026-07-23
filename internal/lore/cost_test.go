package lore

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestCostResourcesDefaults(t *testing.T) {
	res := CostResources(config.LoreConfig{})
	if len(res) != 2 {
		t.Fatalf("len = %d, want 2", len(res))
	}
	if res[0].TypeName != cloud.TypeAWSEC2Instance || res[0].Name != "m5.xlarge" {
		t.Errorf("instance = %+v", res[0])
	}
	if res[1].TypeName != cloud.TypeAWSEC2Volume || res[1].Name != "gp3-500GiB" {
		t.Errorf("volume = %+v", res[1])
	}
}

func TestCostResourcesExplicit(t *testing.T) {
	res := CostResources(config.LoreConfig{
		InstanceType: "m5.2xlarge",
		VolumeSize:   1000,
	})
	if res[0].Name != "m5.2xlarge" {
		t.Errorf("instance = %q", res[0].Name)
	}
	if res[1].Name != "gp3-1000GiB" {
		t.Errorf("volume = %q", res[1].Name)
	}
}
