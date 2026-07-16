package cost

import (
	"fmt"
	"io"
)

// defaultWarnPct is the warn threshold (percent of Monthly) used when a
// BudgetThreshold sets WarnPct to 0.
const defaultWarnPct = 80

// BudgetThreshold is a local budget guardrail evaluated against an estimate.
// It mirrors config.BudgetThreshold; costsource maps between them at the
// command boundary to keep internal/cost free of the config dependency.
type BudgetThreshold struct {
	Scope   string
	Monthly float64
	WarnPct int
}

// BudgetState is the outcome of comparing an estimate to a threshold.
type BudgetState int

const (
	BudgetOK   BudgetState = iota // under the warn line
	BudgetWarn                    // at/over warn line, under threshold
	BudgetOver                    // at/over threshold
)

func (s BudgetState) String() string {
	switch s {
	case BudgetOver:
		return "OVER"
	case BudgetWarn:
		return "WARN"
	default:
		return "OK"
	}
}

// BudgetStatus is the evaluated result for one threshold.
type BudgetStatus struct {
	Scope     string
	Estimate  float64
	Threshold float64
	WarnPct   int
	State     BudgetState
	NoMatch   bool // scope had no matching estimate (evaluated against 0)
}

// EvaluateBudgets compares each threshold against perScope estimates. A scope
// with no estimate evaluates against 0 (OK) and is flagged NoMatch. Over when
// estimate >= threshold; Warn when estimate >= threshold*WarnPct/100.
func EvaluateBudgets(perScope map[string]float64, thresholds []BudgetThreshold) []BudgetStatus {
	out := make([]BudgetStatus, 0, len(thresholds))
	for _, t := range thresholds {
		warnPct := t.WarnPct
		if warnPct <= 0 {
			warnPct = defaultWarnPct
		}
		est, ok := perScope[t.Scope]
		state := BudgetOK
		switch {
		case t.Monthly > 0 && est >= t.Monthly:
			state = BudgetOver
		case t.Monthly > 0 && est >= t.Monthly*float64(warnPct)/100:
			state = BudgetWarn
		}
		out = append(out, BudgetStatus{
			Scope:     t.Scope,
			Estimate:  est,
			Threshold: t.Monthly,
			WarnPct:   warnPct,
			State:     state,
			NoMatch:   !ok,
		})
	}
	return out
}

// RenderBudgets writes a budget-check table.
func RenderBudgets(out io.Writer, statuses []BudgetStatus) {
	fmt.Fprintln(out, "Budget check (warn at configured % of threshold)")
	for _, s := range statuses {
		note := ""
		if s.NoMatch {
			note = "  (no matching resources)"
		} else if s.Threshold > 0 {
			note = fmt.Sprintf("  (%.0f%% of budget)", s.Estimate/s.Threshold*100)
		}
		fmt.Fprintf(out, "  %-10s $%.2f / $%.2f   [%s]%s\n", s.Scope, s.Estimate, s.Threshold, s.State, note)
	}
}
