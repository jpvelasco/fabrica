package schedule

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
)

func TestBuildPlanOff(t *testing.T) {
	p, err := BuildPlan(false, config.CapacitySchedule{}, "start", "stop")
	if err != nil {
		t.Fatal(err)
	}
	if p.Enabled || p.CostFactor != 1 {
		t.Fatalf("off plan = %+v", p)
	}
}

func TestBuildPlanSpotAndWindow(t *testing.T) {
	p, err := BuildPlan(true, config.CapacitySchedule{
		Enabled: true,
		Days:    "Mon-Fri",
		Start:   "08:00",
		Stop:    "20:00",
	}, "fabrica horde agents create", "fabrica horde agents destroy")
	if err != nil {
		t.Fatal(err)
	}
	if p.OnHours != 60 || p.DutyCycle <= 0 || p.CostFactor >= SpotDiscount {
		t.Fatalf("plan = %+v", p)
	}
	if p.StartHint == "" || p.DestroyNote == "" {
		t.Fatal("missing operator hints")
	}
}

func TestBuildPlanErrors(t *testing.T) {
	if _, err := BuildPlan(false, config.CapacitySchedule{Enabled: true, Timezone: "Not/AZone"}, "", ""); err == nil {
		t.Fatal("expected timezone error")
	}
	if _, err := BuildPlan(false, config.CapacitySchedule{Enabled: true, Days: "Tue"}, "", ""); err == nil {
		t.Fatal("expected days error")
	}
	if _, err := BuildPlan(false, config.CapacitySchedule{Enabled: true, Start: "20:00", Stop: "08:00"}, "", ""); err == nil {
		t.Fatal("expected stop-after-start error")
	}
}

func TestEncodeDecodeFactor(t *testing.T) {
	if EncodeFactor("m5.xlarge", 1) != "m5.xlarge" {
		t.Fatal("identity")
	}
	enc := EncodeFactor("m5.xlarge", 0.3)
	base, f := DecodeFactor(enc)
	if base != "m5.xlarge" || f < 0.29 || f > 0.31 {
		t.Fatalf("decode %q = %s %v", enc, base, f)
	}
	if b, g := DecodeFactor("m5.xlarge"); b != "m5.xlarge" || g != 1 {
		t.Fatalf("plain decode = %s %v", b, g)
	}
}

func TestCostFactor(t *testing.T) {
	if CostFactor(false, config.CapacitySchedule{}) != 1 {
		t.Fatal("unused factor")
	}
	if CostFactor(true, config.CapacitySchedule{}) != SpotDiscount {
		t.Fatal("spot-only factor")
	}
	if CostFactor(true, config.CapacitySchedule{Enabled: true, Timezone: "Not/AZone"}) != 1 {
		t.Fatal("invalid schedule should not discount")
	}
}
