package cost

import (
	"fmt"
	"io"
)

// daysPerMonth is the average calendar month length, used to convert a monthly
// estimate into a daily burn rate.
const daysPerMonth = 30.44

// Forecast projects a monthly cost estimate over a time horizon.
type Forecast struct {
	MonthlyEstimate float64
	Days            int
	DailyBurn       float64 // MonthlyEstimate / daysPerMonth
	HorizonCost     float64 // DailyBurn * Days
	Annualized      float64 // MonthlyEstimate * 12
	Confidence      ConfidenceLevel
}

// Project builds a Forecast from a monthly estimate over the given horizon.
// days must be > 0 (the command layer defaults days <= 0 to 30 before calling).
func Project(monthly float64, days int, conf ConfidenceLevel) Forecast {
	daily := monthly / daysPerMonth
	return Forecast{
		MonthlyEstimate: monthly,
		Days:            days,
		DailyBurn:       daily,
		HorizonCost:     daily * float64(days),
		Annualized:      monthly * 12,
		Confidence:      conf,
	}
}

// Render writes the forecast as a small labeled table.
func (f Forecast) Render(out io.Writer) {
	fmt.Fprintf(out, "Cost forecast (%d days) - based on current monthly estimate $%.2f\n", f.Days, f.MonthlyEstimate)
	fmt.Fprintf(out, "  Daily burn:   $%.2f\n", f.DailyBurn)
	fmt.Fprintf(out, "  %d-day cost:  $%.2f\n", f.Days, f.HorizonCost)
	fmt.Fprintf(out, "  Annualized:   $%.2f\n", f.Annualized)
	fmt.Fprintf(out, "Confidence: %s\n", f.Confidence)
}
