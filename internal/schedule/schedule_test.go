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

func TestBuildPlanDefaultsAndDayAliases(t *testing.T) {
	p, err := BuildPlan(false, config.CapacitySchedule{Enabled: true}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if p.Timezone != DefaultTimezone || p.Days != DefaultDays || p.Start != DefaultStart || p.Stop != DefaultStop {
		t.Fatalf("defaults = %+v", p)
	}
	for _, days := range []string{"weekdays", "Mon-Sun", "daily", "weekend", "Sat-Sun"} {
		if _, err := BuildPlan(false, config.CapacitySchedule{Enabled: true, Days: days, Start: "08:00", Stop: "09:00"}, "", ""); err != nil {
			t.Fatalf("days %q: %v", days, err)
		}
	}
	if _, err := BuildPlan(false, config.CapacitySchedule{Enabled: true, Start: "08:00pm", Stop: "20:00"}, "", ""); err == nil {
		t.Fatal("expected start parse error")
	}
	if _, err := BuildPlan(false, config.CapacitySchedule{Enabled: true, Start: "08:00", Stop: "nope"}, "", ""); err == nil {
		t.Fatal("expected stop parse error")
	}
}

func TestEncodeFactorZero(t *testing.T) {
	if EncodeFactor("x", 0) != "x" {
		t.Fatal("zero factor should not encode")
	}
	base, f := DecodeFactor("broken *nope")
	if base != "broken *nope" || f != 1 {
		t.Fatalf("bad suffix decode = %s %v", base, f)
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
	got, err := CostFactor(false, config.CapacitySchedule{})
	if err != nil || got != 1 {
		t.Fatalf("unused factor = %v %v", got, err)
	}
	got, err = CostFactor(true, config.CapacitySchedule{})
	if err != nil || got != SpotDiscount {
		t.Fatalf("spot-only factor = %v %v", got, err)
	}
	got, err = CostFactor(true, config.CapacitySchedule{Enabled: true, Timezone: "Not/AZone"})
	if err == nil || got != 1 {
		t.Fatalf("invalid schedule = %v %v", got, err)
	}
}
