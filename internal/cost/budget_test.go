package cost

import (
	"bytes"
	"strings"
	"testing"
)

func TestEvaluateBudgets(t *testing.T) {
	perScope := map[string]float64{
		"total":    367.48,
		"perforce": 180.16,
		"horde":    187.32,
	}
	thresholds := []BudgetThreshold{
		{Scope: "total", Monthly: 400},              // 91.8% -> Warn (default 80)
		{Scope: "perforce", Monthly: 150},           // over -> Over
		{Scope: "horde", Monthly: 250, WarnPct: 90}, // 74.9% -> OK
		{Scope: "deploy", Monthly: 100},             // no estimate -> OK + NoMatch
	}
	got := EvaluateBudgets(perScope, thresholds)
	if len(got) != 4 {
		t.Fatalf("want 4 statuses, got %d", len(got))
	}
	byScope := map[string]BudgetStatus{}
	for _, s := range got {
		byScope[s.Scope] = s
	}
	if byScope["total"].State != BudgetWarn {
		t.Errorf("total: want Warn, got %v", byScope["total"].State)
	}
	if byScope["perforce"].State != BudgetOver {
		t.Errorf("perforce: want Over, got %v", byScope["perforce"].State)
	}
	if byScope["horde"].State != BudgetOK {
		t.Errorf("horde: want OK, got %v", byScope["horde"].State)
	}
	if byScope["deploy"].State != BudgetOK || !byScope["deploy"].NoMatch {
		t.Errorf("deploy: want OK+NoMatch, got %v NoMatch=%v", byScope["deploy"].State, byScope["deploy"].NoMatch)
	}
}

func TestEvaluateBudgetsBoundaries(t *testing.T) {
	// estimate == threshold -> Over (>=). estimate == warn line -> Warn (>=).
	got := EvaluateBudgets(map[string]float64{"a": 100, "b": 80}, []BudgetThreshold{
		{Scope: "a", Monthly: 100},              // exactly at threshold -> Over
		{Scope: "b", Monthly: 100, WarnPct: 80}, // exactly at warn line -> Warn
	})
	m := map[string]BudgetState{}
	for _, s := range got {
		m[s.Scope] = s.State
	}
	if m["a"] != BudgetOver {
		t.Errorf("a: want Over at threshold, got %v", m["a"])
	}
	if m["b"] != BudgetWarn {
		t.Errorf("b: want Warn at warn line, got %v", m["b"])
	}
}

func TestRenderBudgets(t *testing.T) {
	var b bytes.Buffer
	RenderBudgets(&b, EvaluateBudgets(
		map[string]float64{"total": 90},
		[]BudgetThreshold{{Scope: "total", Monthly: 100}},
	))
	if !strings.Contains(b.String(), "WARN") {
		t.Fatalf("expected WARN in render:\n%s", b.String())
	}
}
