package schedule

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/internal/config"
)

// PrintPlan writes the Spot/schedule plan as JSON or text.
func PrintPlan(out io.Writer, jsonOut, spot bool, cfg config.CapacitySchedule, start, stop string) error {
	plan, err := BuildPlan(spot, cfg, start, stop)
	if err != nil {
		return err
	}
	if jsonOut {
		data, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(out, string(data))
		return nil
	}
	fmt.Fprintf(out, "Spot:     %v\n", plan.Spot)
	fmt.Fprintf(out, "Schedule: %v\n", plan.Enabled)
	if plan.Enabled {
		fmt.Fprintf(out, "  %s %s–%s %s (%.0fh/week, duty %.0f%%)\n", plan.Days, plan.Start, plan.Stop, plan.Timezone, plan.OnHours, plan.DutyCycle*100)
	}
	fmt.Fprintf(out, "Cost factor: %.2f\n", plan.CostFactor)
	fmt.Fprintf(out, "Start: %s\n", plan.StartHint)
	fmt.Fprintf(out, "Stop:  %s\n", plan.StopHint)
	fmt.Fprintln(out, plan.DestroyNote)
	fmt.Fprintln(out, plan.Note)
	return nil
}
