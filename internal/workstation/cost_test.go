package workstation

import (
	"fmt"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
)

func TestCostResourcesDefaults(t *testing.T) {
	got := CostResources(config.WorkstationConfig{}) // empty -> defaults
	if len(got) != 2 {
		t.Fatalf("want 2 resources, got %d: %+v", len(got), got)
	}
	if got[0].TypeName != typeEC2Instance || got[0].Name != DefaultInstanceType {
		t.Errorf("instance: got %+v, want %s", got[0], DefaultInstanceType)
	}
	expectedVolName := "gp3-" + fmt.Sprint(DefaultVolumeSize) + "GiB"
	if got[1].TypeName != typeEC2Volume || got[1].Name != expectedVolName {
		t.Errorf("volume: got %+v, want %s", got[1], expectedVolName)
	}
}

func TestCostResourcesOverrides(t *testing.T) {
	got := CostResources(config.WorkstationConfig{InstanceType: "g5.xlarge", VolumeSize: 250})
	if got[0].Name != "g5.xlarge" || got[1].Name != "gp3-250GiB" {
		t.Fatalf("overrides not applied: %+v", got)
	}
}
