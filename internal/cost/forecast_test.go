package cost

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 0.01 }

func TestProject(t *testing.T) {
	f := Project(367.48, 30, High)
	if f.MonthlyEstimate != 367.48 {
		t.Fatalf("monthly: got %v", f.MonthlyEstimate)
	}
	if !approx(f.DailyBurn, 367.48/30.44) {
		t.Fatalf("daily burn: got %v", f.DailyBurn)
	}
	if !approx(f.HorizonCost, f.DailyBurn*30) {
		t.Fatalf("horizon: got %v", f.HorizonCost)
	}
	if !approx(f.Annualized, 367.48*12) {
		t.Fatalf("annualized: got %v", f.Annualized)
	}
	if f.Days != 30 || f.Confidence != High {
		t.Fatalf("days/conf: %d/%v", f.Days, f.Confidence)
	}
}

func TestProjectZeroMonthly(t *testing.T) {
	f := Project(0, 30, High)
	if f.DailyBurn != 0 || f.HorizonCost != 0 || f.Annualized != 0 {
		t.Fatalf("zero monthly should yield zero burn: %+v", f)
	}
}

func TestForecastRender(t *testing.T) {
	var b bytes.Buffer
	Project(367.48, 30, High).Render(&b)
	out := b.String()
	for _, want := range []string{"Daily burn", "30-day", "Annualized", "Confidence: high"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q:\n%s", want, out)
		}
	}
}
