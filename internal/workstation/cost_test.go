package workstation

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/schedule"
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

func TestCostResourcesSpotEncodesFactor(t *testing.T) {
	got := CostResources(config.WorkstationConfig{Spot: true})
	if got[0].Name == DefaultInstanceType {
		t.Fatalf("spot instance name should encode factor: %s", got[0].Name)
	}
	if got[1].Name != "gp3-"+fmt.Sprint(DefaultVolumeSize)+"GiB" {
		t.Fatalf("volume should stay undiscounted: %s", got[1].Name)
	}
}

func TestCostResourcesOverrides(t *testing.T) {
	got := CostResources(config.WorkstationConfig{InstanceType: "g5.xlarge", VolumeSize: 250})
	if got[0].Name != "g5.xlarge" || got[1].Name != "gp3-250GiB" {
		t.Fatalf("overrides not applied: %+v", got)
	}
}

func TestCostResourcesForShapeSpot(t *testing.T) {
	got := CostResourcesForShape(config.WorkstationConfig{Spot: true}, "g4dn.xlarge", 100)
	base, factor := schedule.DecodeFactor(got[0].Name)
	if base != "g4dn.xlarge" || factor >= 0.999 {
		t.Fatalf("spot instance should carry the encoded factor at the recorded shape: %s", got[0].Name)
	}
	if got[1].Name != "gp3-100GiB" {
		t.Fatalf("volume should stay undiscounted: %s", got[1].Name)
	}
}

func TestCostResourcesForShapeSchedule(t *testing.T) {
	cfg := config.WorkstationConfig{
		Schedule: config.CapacitySchedule{Enabled: true, Timezone: "UTC", Days: "Mon-Fri", Start: "09:00", Stop: "17:00"},
	}
	got := CostResourcesForShape(cfg, "c7i.xlarge", 250)
	base, factor := schedule.DecodeFactor(got[0].Name)
	if base != "c7i.xlarge" || factor >= 0.999 {
		t.Fatalf("scheduled instance should carry the duty-cycle factor: %s", got[0].Name)
	}
}

func TestCostResourcesForShapeInvalidSchedule(t *testing.T) {
	cfg := config.WorkstationConfig{
		Schedule: config.CapacitySchedule{Enabled: true, Days: "Mon-Fri", Start: "17:00", Stop: "09:00"},
	}
	got := CostResourcesForShape(cfg, "g5.xlarge", 100)
	if !strings.HasSuffix(got[0].Name, " (schedule invalid — estimate not discounted)") {
		t.Fatalf("invalid schedule should name the discount refusal: %s", got[0].Name)
	}
	if _, factor := schedule.DecodeFactor(got[0].Name); factor != 1 {
		t.Fatalf("invalid schedule must not discount: %s", got[0].Name)
	}
}

func TestCostResourcesForShapeNoDiscount(t *testing.T) {
	got := CostResourcesForShape(config.WorkstationConfig{}, "g5.xlarge", 250)
	if got[0].Name != "g5.xlarge" || got[1].Name != "gp3-250GiB" {
		t.Fatalf("no spot/schedule should price the shape plainly: %+v", got)
	}
}
