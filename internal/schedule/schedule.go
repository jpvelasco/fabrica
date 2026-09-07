// Package schedule is the shared weekly on/off window used by Horde agents
// and workstations. V1 documents the window and discounts cost; it does
// not install EventBridge or start/stop instances.
package schedule

import (
	"fmt"
	"strings"
	"time"

	"github.com/jpvelasco/fabrica/internal/config"
)

const (
	DefaultTimezone = "UTC"
	DefaultDays     = "Mon-Fri"
	DefaultStart    = "08:00"
	DefaultStop     = "20:00"
	// SpotDiscount is the conservative on-demand fraction used when Spot is on.
	SpotDiscount = 0.30
)

// Plan is the operator-facing schedule + Spot document.
type Plan struct {
	Enabled     bool    `json:"enabled"`
	Spot        bool    `json:"spot"`
	Timezone    string  `json:"timezone,omitempty"`
	Days        string  `json:"days,omitempty"`
	Start       string  `json:"start,omitempty"`
	Stop        string  `json:"stop,omitempty"`
	OnHours     float64 `json:"onHoursPerWeek,omitempty"`
	DutyCycle   float64 `json:"dutyCycle,omitempty"`
	SpotFactor  float64 `json:"spotFactor"`
	CostFactor  float64 `json:"costFactor"`
	StartHint   string  `json:"startHint"`
	StopHint    string  `json:"stopHint"`
	DestroyNote string  `json:"destroyNote"`
	Note        string  `json:"note"`
}

// BuildPlan validates cfg and returns the documented window.
func BuildPlan(spot bool, cfg config.CapacitySchedule, startHint, stopHint string) (Plan, error) {
	p := Plan{
		Enabled:     cfg.Enabled,
		Spot:        spot,
		SpotFactor:  1,
		CostFactor:  1,
		StartHint:   startHint,
		StopHint:    stopHint,
		DestroyNote: "Destroy still tears the resources down regardless of schedule or Spot.",
		Note:        "Fabrica does not install EventBridge and does not request Spot capacity — spot: true only adjusts the estimate. Wire the start/stop hints and Spot request to your own tooling.",
	}
	if spot {
		p.SpotFactor = SpotDiscount
		p.CostFactor = SpotDiscount
	}
	if !cfg.Enabled {
		if !spot {
			p.Note = "No Spot or schedule configured."
		}
		return p, nil
	}
	tz := strings.TrimSpace(cfg.Timezone)
	if tz == "" {
		tz = DefaultTimezone
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return Plan{}, fmt.Errorf("schedule.timezone %q is not a valid IANA timezone: %w", tz, err)
	}
	days := strings.TrimSpace(cfg.Days)
	if days == "" {
		days = DefaultDays
	}
	start := strings.TrimSpace(cfg.Start)
	if start == "" {
		start = DefaultStart
	}
	stop := strings.TrimSpace(cfg.Stop)
	if stop == "" {
		stop = DefaultStop
	}
	onHours, err := weeklyOnHours(days, start, stop)
	if err != nil {
		return Plan{}, err
	}
	p.Timezone = tz
	p.Days = days
	p.Start = start
	p.Stop = stop
	p.OnHours = onHours
	p.DutyCycle = onHours / (7 * 24)
	p.CostFactor = p.SpotFactor * p.DutyCycle
	return p, nil
}

// factorSep separates a resource name from its encoded cost multiplier.
const factorSep = " *"

// EncodeFactor appends a cost multiplier to a resource name when factor < 1.
func EncodeFactor(name string, factor float64) string {
	if factor >= 0.999 || factor <= 0 {
		return name
	}
	return fmt.Sprintf("%s%s%.4f", name, factorSep, factor)
}

// DecodeFactor splits an EncodeFactor name. Missing suffix → factor 1.
func DecodeFactor(name string) (string, float64) {
	i := strings.LastIndex(name, factorSep)
	if i < 0 {
		return name, 1
	}
	var f float64
	if _, err := fmt.Sscanf(name[i+len(factorSep):], "%f", &f); err != nil || f <= 0 {
		return name, 1
	}
	return name[:i], f
}

// CostFactor is the combined Spot × duty-cycle multiplier (1 when unused).
// An invalid schedule returns 1 and an error so cost callers can surface it.
func CostFactor(spot bool, cfg config.CapacitySchedule) (float64, error) {
	p, err := BuildPlan(spot, cfg, "", "")
	if err != nil {
		return 1, fmt.Errorf("invalid capacity schedule (cost estimate not discounted): %w", err)
	}
	return p.CostFactor, nil
}

func weeklyOnHours(days, start, stop string) (float64, error) {
	n, err := parseDays(days)
	if err != nil {
		return 0, err
	}
	sh, sm, err := parseClock(start)
	if err != nil {
		return 0, fmt.Errorf("schedule.start: %w", err)
	}
	eh, em, err := parseClock(stop)
	if err != nil {
		return 0, fmt.Errorf("schedule.stop: %w", err)
	}
	startMin := sh*60 + sm
	stopMin := eh*60 + em
	if stopMin <= startMin {
		return 0, fmt.Errorf("schedule.stop must be after schedule.start (got %s → %s)", start, stop)
	}
	return float64(n) * float64(stopMin-startMin) / 60, nil
}

func parseClock(s string) (int, int, error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, 0, fmt.Errorf("%q is not HH:MM", s)
	}
	return t.Hour(), t.Minute(), nil
}

func parseDays(s string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "mon-fri", "weekdays":
		return 5, nil
	case "mon-sun", "everyday", "daily":
		return 7, nil
	case "sat-sun", "weekend":
		return 2, nil
	default:
		return 0, fmt.Errorf("schedule.days %q is unsupported (want Mon-Fri, Mon-Sun, or Sat-Sun)", s)
	}
}
