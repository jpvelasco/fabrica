# Cost Management Module Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the `fabrica cost` command family (`report`, `forecast`, `alerts list|set|check`) for offline, config-derived cost visibility and local budget guardrails across all provisioned modules.

**Architecture:** Fully offline — reads the local `.fabrica/state.json` cache (which modules exist) and derives cost inputs from the current `fabrica.yaml` (what those modules cost). No live cloud provider. Pure additions to `internal/cost`, a per-module `CostResources(cfg)` extraction, a shared `cmd/internal/costsource` aggregation engine, and `cmd/cost/*` Cobra commands.

**Tech Stack:** Go 1.25.11, Cobra, Viper (scoped to `internal/config`), `go.yaml.in/yaml/v3`. `fmt.Print*` for output — no logging library. Standard `testing` package.

## Global Constraints

- Go 1.25.11+; `github.com/jpvelasco/fabrica` module path.
- `internal/cost` imports no AWS SDK and no `internal/config` (stays provider-agnostic and dependency-free).
- Dependency flow: `cmd/cost → internal/{config, state, cost}` + `cmd/internal/costsource`. `cmd/internal/*` importable only within `cmd/`.
- `cost.Global.Register` panics on duplicate `TypeName` — do NOT register `AWS::EC2::Instance` or `AWS::EC2::Volume` from a new package (already in `internal/perforce/cost.go`).
- Cost commands require NO live provider — use `rt.Config` + local state (`provision.ReadState`) only.
- `ConfidenceLevel`: `High=0, Medium=1, Low=2`. "Least confident wins" rollup is `max()` over the int values (matches `EstimateAll`: `if c > report.Confidence`).
- Every `cost report`/`forecast`/`check` output includes the caveat: *"estimates reflect current fabrica.yaml; run `<module> status` to reconcile."*
- All subcommands support `--json` (root persistent flag). `alerts set` honors `--dry-run` (prints change, writes nothing).
- `alerts check` exit code stays 0 in V1 (informational, not a gate).
- Naming: `snake_case.go` files, `New*` returns pointers, single-letter receivers, acronyms uppercase (`ID`, `ARN`, `URL`).
- Two-file test pattern per `cmd/cost/*` package: white-box `*_test.go` (call `command.run()` with injected seams) + black-box `cobra_test.go` (minimal root replicating `--json`/`--dry-run` persistent flags).
- Coverage target: 60%+ for new `internal/*` and `cmd/internal/*` code.

---

## File Structure

**New files:**
- `internal/cost/forecast.go` — `Forecast` type + `Project(monthly, days, conf)`; `Forecast.Render`.
- `internal/cost/budget.go` — `BudgetThreshold`, `BudgetState`, `BudgetStatus`, `EvaluateBudgets`; budget-table renderer.
- `internal/perforce/cost.go` (modify) — add `CostResources(config.PerforceConfig) []cost.Resource`.
- `internal/horde/cost.go` — add `CostResources(config.HordeConfig) []cost.Resource`.
- `internal/workstation/cost.go` — add `CostResources(config.WorkstationConfig) []cost.Resource`.
- `internal/ci/cost.go` (modify) — add `CostResources(config.CIConfig) []cost.Resource`.
- `internal/deploy/cost.go` (modify) — add `CostResources(config.DeployConfig) []cost.Resource` (fleet line only).
- `cmd/internal/costsource/costsource.go` — `ModuleCost`, `Breakdown`, `Aggregate(cfg, st, reg)`.
- `cmd/cost/cost.go` — parent command wiring the three children.
- `cmd/cost/report/report.go` — `cost report`.
- `cmd/cost/forecast/forecast.go` — `cost forecast --days N`.
- `cmd/cost/alerts/alerts.go` — `alerts` parent + `list`/`set`/`check` sub-subcommands.
- Test files alongside each (`*_test.go` + `cobra_test.go` for cmd packages).

**Modified files:**
- `internal/config/config.go` — replace `Cost any` with typed `CostConfig`/`BudgetThreshold`; wire through `Config`, `fileConfig`, `fileConfig()`; drop `emptySection` for cost.
- `internal/{perforce,horde,workstation,ci,deploy}/plan.go` — refactor `NewCreatePlan`/`NewSetupPlan` to call the new `CostResources` helper (single source of truth).
- `cmd/root/root.go` — register `cost.New(...)`.
- `ROADMAP.md`, `CLAUDE.md`, `fabrica.example.yaml` — docs.

**Build order:** config → internal/cost pure additions → per-module CostResources → costsource → cmd/cost → docs. Each layer compiles and tests green before the next.

---

### Task 1: Typed `CostConfig` in `internal/config`

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing (leaf).
- Produces: `config.CostConfig{ Budgets []config.BudgetThreshold }`; `config.BudgetThreshold{ Scope string; Monthly float64; WarnPct int }`. `Config.Cost` is now `CostConfig` (was `any`).

- [ ] **Step 1: Write the failing test**

Add to `internal/config/config_test.go`:

```go
func TestCostConfigRoundTrip(t *testing.T) {
	c := Defaults()
	c.Cost.Budgets = []BudgetThreshold{
		{Scope: "total", Monthly: 400, WarnPct: 80},
		{Scope: "perforce", Monthly: 150},
	}
	data, err := c.YAML()
	if err != nil {
		t.Fatalf("YAML: %v", err)
	}
	if !strings.Contains(string(data), "budgets:") {
		t.Fatalf("expected budgets in YAML, got:\n%s", data)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "fabrica.yaml")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Cost.Budgets) != 2 {
		t.Fatalf("want 2 budgets, got %d", len(loaded.Cost.Budgets))
	}
	if loaded.Cost.Budgets[0].Scope != "total" || loaded.Cost.Budgets[0].Monthly != 400 {
		t.Fatalf("unexpected first budget: %+v", loaded.Cost.Budgets[0])
	}
}
```

Ensure the test file imports `strings`, `os`, `path/filepath`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestCostConfigRoundTrip`
Expected: FAIL — `c.Cost.Budgets` undefined (`Cost` is `any`).

- [ ] **Step 3: Implement the typed config**

In `internal/config/config.go`:

1. Add the new types (near the other config structs):

```go
// CostConfig holds the cost: section of fabrica.yaml.
type CostConfig struct {
	Budgets []BudgetThreshold `mapstructure:"budgets" yaml:"budgets"`
}

// BudgetThreshold is a single local budget guardrail. Scope is "total" or a
// module name; Monthly is the USD/month ceiling; WarnPct is the warn threshold
// as a percent of Monthly (0 → engine default of 80).
type BudgetThreshold struct {
	Scope   string  `mapstructure:"scope"   yaml:"scope"`
	Monthly float64 `mapstructure:"monthly" yaml:"monthly"`
	WarnPct int     `mapstructure:"warnPct" yaml:"warnPct,omitempty"`
}
```

2. Change the `Cost` field on `Config` (was `Cost any`):

```go
	Cost        CostConfig        `mapstructure:"cost"        yaml:"cost"`
```

3. Change the `Cost` field on `fileConfig` (was `Cost any`):

```go
	Cost        CostConfig        `yaml:"cost"`
```

4. In `fileConfig()`, replace `Cost: emptySection(c.Cost),` with:

```go
		Cost:        c.Cost,
```

5. Delete the now-unused `emptySection` function.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config/`
Expected: PASS. Then `go build ./...` — Expected: clean (confirms nothing else referenced `emptySection` or `Cost` as `any`).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): typed CostConfig with budget thresholds"
```

---

### Task 2: `Project` / `Forecast` in `internal/cost`

**Files:**
- Create: `internal/cost/forecast.go`
- Test: `internal/cost/forecast_test.go`

**Interfaces:**
- Consumes: `cost.ConfidenceLevel` (existing).
- Produces: `cost.Forecast{ MonthlyEstimate, DailyBurn, HorizonCost, Annualized float64; Days int; Confidence ConfidenceLevel }`; `func Project(monthly float64, days int, conf ConfidenceLevel) Forecast`; `func (Forecast) Render(out io.Writer)`.

- [ ] **Step 1: Write the failing test**

Create `internal/cost/forecast_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cost/ -run TestProject`
Expected: FAIL — `Project` undefined.

- [ ] **Step 3: Implement `forecast.go`**

Create `internal/cost/forecast.go`:

```go
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
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/cost/`
Expected: PASS (existing + new).

- [ ] **Step 5: Commit**

```bash
git add internal/cost/forecast.go internal/cost/forecast_test.go
git commit -m "feat(cost): Project forecast projection"
```

---

### Task 3: `EvaluateBudgets` in `internal/cost`

**Files:**
- Create: `internal/cost/budget.go`
- Test: `internal/cost/budget_test.go`

**Interfaces:**
- Consumes: nothing (leaf; `cost.BudgetThreshold` is defined here, distinct from `config.BudgetThreshold`).
- Produces:
  - `cost.BudgetThreshold{ Scope string; Monthly float64; WarnPct int }`
  - `cost.BudgetState` (`BudgetOK`, `BudgetWarn`, `BudgetOver`) with `String()`.
  - `cost.BudgetStatus{ Scope string; Estimate, Threshold float64; WarnPct int; State BudgetState; NoMatch bool }`
  - `func EvaluateBudgets(perScope map[string]float64, thresholds []BudgetThreshold) []BudgetStatus`
  - `func RenderBudgets(out io.Writer, statuses []BudgetStatus)`

- [ ] **Step 1: Write the failing test**

Create `internal/cost/budget_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cost/ -run TestEvaluateBudgets`
Expected: FAIL — `EvaluateBudgets` undefined.

- [ ] **Step 3: Implement `budget.go`**

Create `internal/cost/budget.go`:

```go
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
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/cost/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cost/budget.go internal/cost/budget_test.go
git commit -m "feat(cost): EvaluateBudgets local threshold evaluation"
```

---

### Task 4: Extract `CostResources(cfg)` per module

Extract the inline `CostResources` construction from each `NewCreatePlan`/`NewSetupPlan` into a pure `CostResources(cfg)` helper, then have the constructor call it. Single source of truth for "what does module X cost at config Y." No behavior change — existing create/plan tests must stay green (that is the refactor-safety check).

**Files:**
- Modify: `internal/perforce/cost.go`, `internal/perforce/plan.go`
- Create: `internal/horde/cost.go`; Modify: `internal/horde/plan.go`
- Create: `internal/workstation/cost.go`; Modify: `internal/workstation/plan.go`
- Modify: `internal/ci/cost.go`, `internal/ci/plan.go`
- Modify: `internal/deploy/cost.go`, `internal/deploy/plan.go`
- Test: add to each module's existing `cost_test.go` (or create `internal/horde/cost_test.go`, `internal/workstation/cost_test.go`).

**Interfaces:**
- Consumes: `config.{Perforce,Horde,Workstation,CI,Deploy}Config`, `cost.Resource`.
- Produces (each in its own package):
  - `perforce.CostResources(config.PerforceConfig) []cost.Resource`
  - `horde.CostResources(config.HordeConfig) []cost.Resource`
  - `workstation.CostResources(config.WorkstationConfig) []cost.Resource`
  - `ci.CostResources(config.CIConfig) []cost.Resource`
  - `deploy.CostResources(config.DeployConfig) []cost.Resource` (fleet line only — the standing monthly cost)

- [ ] **Step 1: Write the failing test (perforce first)**

Add to `internal/perforce/cost_test.go`:

```go
func TestCostResourcesDefaults(t *testing.T) {
	got := CostResources(config.PerforceConfig{}) // empty -> defaults
	if len(got) != 2 {
		t.Fatalf("want 2 resources, got %d: %+v", len(got), got)
	}
	if got[0].TypeName != TypeAWSEC2Instance || got[0].Name != "m5.xlarge" {
		t.Errorf("instance: got %+v", got[0])
	}
	if got[1].TypeName != TypeAWSEC2Volume || got[1].Name != "gp3-500GiB" {
		t.Errorf("volume: got %+v", got[1])
	}
}

func TestCostResourcesOverrides(t *testing.T) {
	got := CostResources(config.PerforceConfig{InstanceType: "m5.2xlarge", VolumeSize: 1000})
	if got[0].Name != "m5.2xlarge" || got[1].Name != "gp3-1000GiB" {
		t.Fatalf("overrides not applied: %+v", got)
	}
}
```

Ensure `internal/perforce/cost_test.go` imports `github.com/jpvelasco/fabrica/internal/config`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/perforce/ -run TestCostResources`
Expected: FAIL — `CostResources` undefined.

- [ ] **Step 3: Implement `perforce.CostResources` + refactor `NewCreatePlan`**

In `internal/perforce/cost.go`, add (needs `config` and `fmt` imports — `fmt` is already imported):

```go
// CostResources returns the cost inputs for a Perforce module at the given
// config, applying the same defaults as NewCreatePlan. Pure — no AWS, no
// validation side effects. Single source of truth for the module cost shape.
func CostResources(cfg config.PerforceConfig) []cost.Resource {
	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = "m5.xlarge"
	}
	volumeSize := cfg.VolumeSize
	if volumeSize <= 0 {
		volumeSize = 500
	}
	return []cost.Resource{
		{TypeName: TypeAWSEC2Instance, Name: instanceType},
		{TypeName: TypeAWSEC2Volume, Name: fmt.Sprintf("gp3-%dGiB", volumeSize)},
	}
}
```

Add the import `"github.com/jpvelasco/fabrica/internal/config"` to `internal/perforce/cost.go`.

In `internal/perforce/plan.go`, replace the inline `CostResources: []cost.Resource{...}` literal in the returned `&CreatePlan{...}` with:

```go
		CostResources: CostResources(cfg),
```

(The `instanceType`/`volumeSize` locals already computed in `NewCreatePlan` still feed the desired-state builders; only the cost literal is replaced. Leave those locals in place.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/perforce/`
Expected: PASS (new `CostResources` tests + unchanged plan tests confirm no behavior change).

- [ ] **Step 5: Repeat for horde, workstation, ci, deploy**

Apply the identical pattern. Exact per-module details:

**horde** — create `internal/horde/cost.go`:

```go
package horde

import (
	"fmt"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

// CostResources returns the cost inputs for a Horde module at the given config,
// applying the same defaults as NewCreatePlan.
func CostResources(cfg config.HordeConfig) []cost.Resource {
	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = "m7i.2xlarge"
	}
	volumeSize := cfg.VolumeSize
	if volumeSize <= 0 {
		volumeSize = 100
	}
	return []cost.Resource{
		{TypeName: TypeAWSEC2Instance, Name: instanceType},
		{TypeName: TypeAWSEC2Volume, Name: fmt.Sprintf("gp3-%dGiB", volumeSize)},
	}
}
```

Then in `internal/horde/plan.go` replace the cost literal with `CostResources: CostResources(cfg),`. Add `internal/horde/cost_test.go` mirroring the perforce tests (defaults: `m7i.2xlarge`, `gp3-100GiB`).

**workstation** — create `internal/workstation/cost.go`. Cost inputs depend only on instance type + volume size, and the template/config precedence lives in `NewCreatePlan`. To keep one source of truth, extract a small helper both call:

```go
package workstation

import (
	"fmt"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

// resolveSizing applies template + config + default precedence for the two
// cost-relevant fields. tmpl is "", TemplateArtist, or TemplateProgrammer.
func resolveSizing(cfg config.WorkstationConfig, tmpl string) (instanceType string, volumeSize int) {
	instanceType = cfg.InstanceType
	volumeSize = cfg.VolumeSize
	switch tmpl {
	case TemplateArtist:
		instanceType, volumeSize = ArtistInstanceType, ArtistVolumeSize
	case TemplateProgrammer:
		instanceType, volumeSize = ProgrammerInstanceType, ProgrammerVolumeSize
	}
	if instanceType == "" {
		instanceType = DefaultInstanceType
	}
	if volumeSize <= 0 {
		volumeSize = DefaultVolumeSize
	}
	return instanceType, volumeSize
}

// CostResources returns the cost inputs for a workstation at the given config.
// The cost path uses no template (tmpl=""), so it reflects config + defaults;
// a template only applies at create time.
func CostResources(cfg config.WorkstationConfig) []cost.Resource {
	instanceType, volumeSize := resolveSizing(cfg, "")
	return []cost.Resource{
		{TypeName: typeEC2Instance, Name: instanceType},
		{TypeName: typeEC2Volume, Name: fmt.Sprintf("gp3-%dGiB", volumeSize)},
	}
}
```

Then refactor `NewCreatePlan` in `internal/workstation/plan.go`: replace the `switch tmpl {...}` sizing block AND the two `if instanceType == ""` / `if volumeSize <= 0` defaulting blocks with a single `resolveSizing` call — BUT preserve the unknown-template error. Concretely, validate `tmpl` up front, then call the helper:

```go
	switch tmpl {
	case "", TemplateArtist, TemplateProgrammer:
		// valid
	default:
		return nil, fmt.Errorf("unknown template %q: must be %q or %q", tmpl, TemplateArtist, TemplateProgrammer)
	}
	instanceType, volumeSize := resolveSizing(cfg, tmpl)
```

Replace the cost literal with `CostResources: CostResources(cfg),`. Add `internal/workstation/cost_test.go` (defaults resolve to `DefaultInstanceType` + `gp3-<DefaultVolumeSize>GiB`).

**ci** — in `internal/ci/cost.go` add:

```go
// CostResources returns the cost inputs for the CI module at the given config,
// applying the same defaults as NewCreatePlan.
func CostResources(cfg config.CIConfig) []cost.Resource {
	projectName := cfg.ProjectName
	if projectName == "" {
		projectName = defaultProjectName
	}
	computeType := cfg.ComputeType
	if computeType == "" {
		computeType = defaultComputeType
	}
	return []cost.Resource{
		{TypeName: TypeAWSIAMRole, Name: defaultRoleName},
		{TypeName: TypeAWSCodeBuildProject, Name: projectName + " (" + computeType + ")"},
	}
}
```

Requires adding the `config` import to `internal/ci/cost.go` if absent. Then in `internal/ci/plan.go` replace the cost literal with `CostResources: CostResources(cfg),`. Add a `TestCostResources` to `internal/ci/cost_test.go`.

**deploy** — in `internal/deploy/cost.go` add (fleet line ONLY; IAM role + alias are ~free and excluded from standing cost):

```go
// CostResources returns the standing monthly cost inputs for the deploy module:
// the active fleet. The IAM role and alias are ~free and excluded. The engine
// includes this only when a fleet resource actually exists in state.
func CostResources(cfg config.DeployConfig) []cost.Resource {
	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = defaultInstanceType
	}
	desired := cfg.DesiredInstances
	if desired <= 0 {
		desired = defaultDesiredInstances
	}
	return []cost.Resource{
		{TypeName: TypeGameLiftFleet, Name: fleetCostName(instanceType, desired)},
	}
}
```

Requires the `config` import in `internal/deploy/cost.go`. Do NOT refactor `NewPromotePlan`'s literal — promote's cost list also includes the build line and is create-time specific; `CostResources` is the *standing* cost used by the cost engine. Add `TestCostResources` to `internal/deploy/cost_test.go` asserting one fleet line with `fleetCostName(defaultInstanceType, defaultDesiredInstances)`.

- [ ] **Step 6: Run the full suite**

Run: `go test ./internal/...`
Expected: PASS across all five modules. Run `go build ./...` — clean.

- [ ] **Step 7: Commit**

```bash
git add internal/perforce internal/horde internal/workstation internal/ci internal/deploy
git commit -m "refactor: extract CostResources(cfg) helper per module"
```

---

### Task 5: `cmd/internal/costsource` aggregation engine

Single owner of "enumerate provisioned modules → estimated cost breakdown." The module-name switch is the ONLY place that enumerates modules (mirrors how `status` owns its enumeration).

**Files:**
- Create: `cmd/internal/costsource/costsource.go`
- Test: `cmd/internal/costsource/costsource_test.go`

**Interfaces:**
- Consumes: `config.Config`, `state.State`, `cost.Registry`; each module's `CostResources(cfg)` (Task 4); `cost.Report`, `cost.ConfidenceLevel`.
- Produces:
  - `costsource.ModuleCost{ Name, Status string; Report cost.Report; Subtotal float64; Note string }`
  - `costsource.Breakdown{ Modules []ModuleCost; Total float64; Confidence cost.ConfidenceLevel; PerScope map[string]float64 }`
  - `func Aggregate(cfg *config.Config, st *state.State, reg *cost.Registry) Breakdown`
  - `func MapBudgets([]config.BudgetThreshold) []cost.BudgetThreshold`

- [ ] **Step 1: Write the failing test**

Create `cmd/internal/costsource/costsource_test.go`:

```go
package costsource

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/state"
)

func mod(name, status string, res ...state.ModuleResource) state.ModuleState {
	return state.ModuleState{Name: name, Status: status, Resources: res}
}

func TestAggregateMultiModule(t *testing.T) {
	cfg := config.Defaults()
	st := state.NewState("acct", "us-east-1")
	st.Modules = []state.ModuleState{
		mod("perforce", "ready",
			state.ModuleResource{TypeName: "AWS::EC2::Instance", Identifier: "i-1"},
			state.ModuleResource{TypeName: "AWS::EC2::Volume", Identifier: "vol-1"}),
		mod("horde", "ready",
			state.ModuleResource{TypeName: "AWS::EC2::Instance", Identifier: "i-2"},
			state.ModuleResource{TypeName: "AWS::EC2::Volume", Identifier: "vol-2"}),
	}
	b := Aggregate(cfg, st, cost.Global)
	if len(b.Modules) != 2 {
		t.Fatalf("want 2 modules, got %d", len(b.Modules))
	}
	if b.Total <= 0 {
		t.Fatalf("want positive total, got %v", b.Total)
	}
	if b.PerScope["total"] != b.Total {
		t.Fatalf("PerScope total %v != Total %v", b.PerScope["total"], b.Total)
	}
	if _, ok := b.PerScope["perforce"]; !ok {
		t.Fatalf("PerScope missing perforce")
	}
}

func TestAggregateStoppedDropsCompute(t *testing.T) {
	cfg := config.Defaults()
	st := state.NewState("acct", "us-east-1")
	st.Modules = []state.ModuleState{
		mod("workstation", "stopped",
			state.ModuleResource{TypeName: "AWS::EC2::Instance", Identifier: "i-1"},
			state.ModuleResource{TypeName: "AWS::EC2::Volume", Identifier: "vol-1"}),
	}
	cfg.Workstation.AmiID = "ami-123"
	b := Aggregate(cfg, st, cost.Global)
	ws := b.Modules[0]
	// Only the volume line should remain (compute not billed while stopped).
	for _, r := range ws.Report.Results {
		if r.Resource.TypeName == "AWS::EC2::Instance" {
			t.Fatalf("stopped module should drop the instance line: %+v", ws.Report.Results)
		}
	}
	if ws.Note == "" {
		t.Errorf("stopped module should carry a note")
	}
}

func TestAggregateDeployFleetOnlyWhenPresent(t *testing.T) {
	cfg := config.Defaults()
	// Setup-only deploy: role + alias, no fleet.
	st := state.NewState("acct", "us-east-1")
	st.Modules = []state.ModuleState{
		mod("deploy", "ready",
			state.ModuleResource{TypeName: "AWS::IAM::Role", Identifier: "r-1"},
			state.ModuleResource{TypeName: "AWS::GameLift::Alias", Identifier: "a-1"}),
	}
	b := Aggregate(cfg, st, cost.Global)
	if b.Modules[0].Subtotal != 0 {
		t.Fatalf("setup-only deploy should cost ~0, got %v", b.Modules[0].Subtotal)
	}

	// With a fleet present, the fleet cost is included.
	st.Modules[0].Resources = append(st.Modules[0].Resources,
		state.ModuleResource{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-1"})
	b = Aggregate(cfg, st, cost.Global)
	if b.Modules[0].Subtotal <= 0 {
		t.Fatalf("deploy with fleet should have positive subtotal, got %v", b.Modules[0].Subtotal)
	}
}

func TestAggregateUnknownModule(t *testing.T) {
	cfg := config.Defaults()
	st := state.NewState("acct", "us-east-1")
	st.Modules = []state.ModuleState{mod("mystery", "ready")}
	b := Aggregate(cfg, st, cost.Global)
	if len(b.Modules) != 1 || b.Modules[0].Subtotal != 0 {
		t.Fatalf("unknown module should contribute 0: %+v", b.Modules)
	}
	if b.Modules[0].Note == "" {
		t.Errorf("unknown module should carry a 'no estimator wired' note")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/internal/costsource/`
Expected: FAIL — package/`Aggregate` undefined.

- [ ] **Step 3: Implement `costsource.go`**

Create `cmd/internal/costsource/costsource.go`:

```go
// Package costsource is the shared engine that turns provisioned state plus the
// current config into an estimated cost breakdown. It is the single owner of
// module enumeration for the cost commands (report, forecast, alerts), the same
// way modstatus owns status enumeration and teardown owns delete ordering.
//
// It is fully offline: it reads local state (which modules exist) and derives
// cost inputs from config (what those modules are configured as). No AWS SDK,
// no live provider.
package costsource

import (
	"github.com/jpvelasco/fabrica/internal/ci"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/deploy"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/perforce"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/workstation"
)

// ModuleCost is the estimated cost for one provisioned module.
type ModuleCost struct {
	Name     string
	Status   string
	Report   cost.Report
	Subtotal float64
	Note     string
}

// Breakdown is the full cost picture across all provisioned modules.
type Breakdown struct {
	Modules    []ModuleCost
	Total      float64
	Confidence cost.ConfidenceLevel
	PerScope   map[string]float64 // module subtotals + "total"
}

// Aggregate builds the cost breakdown for the modules present in state, using
// cost inputs derived from cfg. The switch on module name is the only place
// that enumerates modules.
func Aggregate(cfg *config.Config, st *state.State, reg *cost.Registry) Breakdown {
	b := Breakdown{
		Confidence: cost.High,
		PerScope:   make(map[string]float64),
	}
	for i := range st.Modules {
		m := &st.Modules[i]
		resources, note := costInputs(cfg, m)
		report := reg.EstimateAll(resources)
		mc := ModuleCost{
			Name:     m.Name,
			Status:   m.Status,
			Report:   report,
			Subtotal: report.Total,
			Note:     note,
		}
		b.Modules = append(b.Modules, mc)
		b.Total += report.Total
		b.PerScope[m.Name] += report.Total
		if report.Confidence > b.Confidence {
			b.Confidence = report.Confidence
		}
	}
	b.PerScope["total"] = b.Total
	return b
}

// costInputs returns the cost resources for a module plus an optional note.
// It applies two state-aware adjustments the raw CostResources helpers cannot:
// stopped instances drop their compute line, and deploy includes the fleet line
// only when a fleet resource actually exists in state.
func costInputs(cfg *config.Config, m *state.ModuleState) ([]cost.Resource, string) {
	switch m.Name {
	case "perforce":
		return applyStopped(perforce.CostResources(cfg.Perforce), m.Status)
	case "horde":
		return applyStopped(horde.CostResources(cfg.Horde), m.Status)
	case "workstation":
		return applyStopped(workstation.CostResources(cfg.Workstation), m.Status)
	case "ci":
		return ci.CostResources(cfg.CI), ""
	case "deploy":
		if !hasResource(m, deploy.TypeGameLiftFleet) {
			return nil, "setup only (no active fleet) — standing cost ~$0"
		}
		return deploy.CostResources(cfg.Deploy), ""
	default:
		return nil, "no estimator wired for this module"
	}
}

// applyStopped drops the EC2 instance (compute) line when the module is stopped;
// EBS volumes are still billed. Returns an explanatory note when it does.
func applyStopped(resources []cost.Resource, status string) ([]cost.Resource, string) {
	if status != "stopped" {
		return resources, ""
	}
	kept := resources[:0:0]
	for _, r := range resources {
		if r.TypeName == "AWS::EC2::Instance" {
			continue
		}
		kept = append(kept, r)
	}
	return kept, "stopped — compute not billed (EBS still billed)"
}

func hasResource(m *state.ModuleState, typeName string) bool {
	for _, r := range m.Resources {
		if r.TypeName == typeName {
			return true
		}
	}
	return false
}

// MapBudgets converts config budget thresholds into cost budget thresholds,
// keeping internal/cost free of the config dependency.
func MapBudgets(in []config.BudgetThreshold) []cost.BudgetThreshold {
	out := make([]cost.BudgetThreshold, 0, len(in))
	for _, b := range in {
		out = append(out, cost.BudgetThreshold{Scope: b.Scope, Monthly: b.Monthly, WarnPct: b.WarnPct})
	}
	return out
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/internal/costsource/`
Expected: PASS. Then `go vet ./cmd/internal/costsource/` and confirm the dependency rule: `go list -deps ./internal/cost/...` must NOT include `internal/config` (costsource does the mapping, cost stays clean).

- [ ] **Step 5: Commit**

```bash
git add cmd/internal/costsource/
git commit -m "feat(costsource): offline cost aggregation engine"
```

---

### Task 6: `cmd/cost` parent + `cost report`

**Files:**
- Create: `cmd/cost/cost.go` (parent), `cmd/cost/report/report.go`
- Test: `cmd/cost/report/report_test.go`, `cmd/cost/cobra_test.go`

**Interfaces:**
- Consumes: `globals.{Runtime,RuntimeSource,OptionsSource,Options}`, `provision.ReadState`, `costsource.Aggregate`, `cost.Global`.
- Produces: `cost.New(runtimeSource, optionsSource, out) *cobra.Command` (parent, wires report/forecast/alerts); `report.New(...) *cobra.Command`.

- [ ] **Step 1: Write the failing test**

Create `cmd/cost/report/report_test.go`:

```go
package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/state"
)

func seededState() *state.State {
	st := state.NewState("acct", "us-east-1")
	st.Modules = []state.ModuleState{{
		Name:   "perforce",
		Status: "ready",
		Resources: []state.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-1"},
			{TypeName: "AWS::EC2::Volume", Identifier: "vol-1"},
		},
	}}
	return st
}

func newTestCommand(out *bytes.Buffer, jsonOut bool) command {
	return command{
		cfg:       config.Defaults(),
		costs:     cost.Global,
		jsonOut:   jsonOut,
		out:       out,
		readState: func() (*state.State, error) { return seededState(), nil },
	}
}

func TestReportText(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, false)
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"perforce", "Total", "Confidence", "fabrica.yaml"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestReportJSON(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, true)
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Total   float64 `json:"total"`
		Modules []struct {
			Name string `json:"name"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if payload.Total <= 0 || len(payload.Modules) != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/cost/report/`
Expected: FAIL — package/`command` undefined.

- [ ] **Step 3: Implement `cmd/cost/report/report.go`**

```go
// Package report implements "fabrica cost report": an offline monthly cost
// estimate broken down by provisioned module, derived from the current
// fabrica.yaml scoped to the modules present in local state.
package report

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/costsource"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const lineWidth = 64

const caveat = "Note: estimates reflect current fabrica.yaml; run `<module> status` to reconcile."

type command struct {
	cfg       *config.Config
	costs     *fabricacost.Registry
	jsonOut   bool
	out       io.Writer
	readState func() (*fabricastate.State, error)
}

// New returns the "cost report" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "report",
		Short: "Show the estimated monthly cost broken down by module",
		Long: `Show the estimated monthly infrastructure cost, broken down by provisioned
module and resource. Fully offline: reads local state for which modules exist
and the current fabrica.yaml for their cost inputs. No AWS calls.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				cfg:       rt.Config,
				costs:     fabricacost.Global,
				jsonOut:   opts.JSONOutput,
				out:       out,
				readState: func() (*fabricastate.State, error) { return provision.ReadState(rt) },
			}
			return c.run()
		},
	}
}

func (c command) run() error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	b := costsource.Aggregate(c.cfg, st, c.costs)
	if c.jsonOut {
		return c.renderJSON(b)
	}
	c.renderText(b)
	return nil
}

func (c command) renderText(b costsource.Breakdown) {
	fmt.Fprintln(c.out, "Cost estimate (monthly) — based on current fabrica.yaml")
	fmt.Fprintln(c.out, dashes())
	if len(b.Modules) == 0 {
		fmt.Fprintln(c.out, "  No provisioned modules found in state.")
	}
	for _, m := range b.Modules {
		fmt.Fprintf(c.out, "  %-14s (%s)\n", m.Name, m.Status)
		for _, r := range m.Report.Results {
			if r.Err != nil {
				fmt.Fprintf(c.out, "    %-22s %10s  %s\n", r.Resource.Name, "-", "(no estimate)")
				continue
			}
			fmt.Fprintf(c.out, "    %-22s $%-9.2f %s\n", r.Resource.Name, r.Monthly.Amount, r.Monthly.Confidence)
		}
		if m.Note != "" {
			fmt.Fprintf(c.out, "    (%s)\n", m.Note)
		}
		fmt.Fprintf(c.out, "    %-22s $%-9.2f\n", "subtotal", m.Subtotal)
	}
	fmt.Fprintln(c.out, dashes())
	fmt.Fprintf(c.out, "  %-22s $%-9.2f\n", "Total:", b.Total)
	fmt.Fprintf(c.out, "Confidence: %s\n", b.Confidence)
	fmt.Fprintln(c.out, caveat)
}

func dashes() string {
	s := make([]byte, lineWidth)
	for i := range s {
		s[i] = '-'
	}
	return string(s)
}

// jsonModule is the JSON shape for one module in the report.
type jsonModule struct {
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	Subtotal float64 `json:"subtotal"`
	Note     string  `json:"note,omitempty"`
}

func (c command) renderJSON(b costsource.Breakdown) error {
	payload := struct {
		Total      float64      `json:"total"`
		Confidence string       `json:"confidence"`
		Modules    []jsonModule `json:"modules"`
		Note       string       `json:"note"`
	}{
		Total:      b.Total,
		Confidence: b.Confidence.String(),
		Note:       caveat,
	}
	for _, m := range b.Modules {
		payload.Modules = append(payload.Modules, jsonModule{
			Name: m.Name, Status: m.Status, Subtotal: m.Subtotal, Note: m.Note,
		})
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	fmt.Fprintln(c.out, string(data))
	return nil
}
```

- [ ] **Step 4: Implement `cmd/cost/cost.go` (parent)**

```go
// Package cost wires the "cost" parent command and its subcommands (report,
// forecast, alerts): offline, config-derived cost visibility and local budget
// guardrails. None of the subcommands require a live cloud provider.
package cost

import (
	"io"

	"github.com/jpvelasco/fabrica/cmd/cost/report"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/spf13/cobra"
)

// New returns the "cost" parent command.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cost",
		Short: "Estimate and monitor infrastructure cost",
		Long: `Offline cost visibility and local budget guardrails across all provisioned
modules. Estimates are derived from the current fabrica.yaml, scoped to the
modules present in local state — no AWS calls.

Available operations:
  report    Estimated monthly cost broken down by module
  forecast  Project the current estimate over a time horizon
  alerts    Manage and check local budget thresholds`,
	}
	cmd.AddCommand(report.New(runtimeSource, optionsSource, out))
	return cmd
}
```

Wire ONLY `report.New` in this task. Task 7 adds the `forecast` import + `cmd.AddCommand(forecast.New(...))` line; Task 8 adds the `alerts` import + line. This keeps each task compiling on its own.

- [ ] **Step 5: Write `cmd/cost/cobra_test.go`**

```go
package cost_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/cost"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(src globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	var opts globals.Options
	root := &cobra.Command{Use: "fabrica", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().BoolVarP(&opts.JSONOutput, "json", "j", false, "")
	root.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "")
	root.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "")
	root.SetOut(out)
	root.SetErr(out)
	optionsSource := func() globals.Options { return opts }
	root.AddCommand(cost.New(src, optionsSource, out))
	return root
}

func TestCostReportWiring(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults()}, nil
	}
	var out bytes.Buffer
	root := buildTestRoot(src, &out)
	root.SetArgs([]string{"cost", "report"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "Cost estimate") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
}
```

Because the cobra test reads state from disk via `provision.ReadState` (no `.fabrica/state.json` in a fresh temp), it exercises the empty-state path. Run it from a clean dir or accept the "No provisioned modules" output — the assertion only checks the header, which always prints. If the working dir has a real `.fabrica/state.json`, use `t.Chdir(t.TempDir())` at the top of the test to isolate.

- [ ] **Step 6: Run tests**

Run: `go test ./cmd/cost/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/cost/
git commit -m "feat(cost): cost report subcommand + parent wiring"
```

---

### Task 7: `cost forecast`

**Files:**
- Create: `cmd/cost/forecast/forecast.go`
- Modify: `cmd/cost/cost.go` (wire the child)
- Test: `cmd/cost/forecast/forecast_test.go`

**Interfaces:**
- Consumes: same as report + `cost.Project`, `cost.Forecast`.
- Produces: `forecast.New(runtimeSource, optionsSource, out) *cobra.Command` with a `--days` flag (default 30).

- [ ] **Step 1: Write the failing test**

Create `cmd/cost/forecast/forecast_test.go`:

```go
package forecast

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/state"
)

func seededState() *state.State {
	st := state.NewState("acct", "us-east-1")
	st.Modules = []state.ModuleState{{
		Name: "perforce", Status: "ready",
		Resources: []state.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-1"},
			{TypeName: "AWS::EC2::Volume", Identifier: "vol-1"},
		},
	}}
	return st
}

func newTestCommand(out *bytes.Buffer, days int, jsonOut bool) command {
	return command{
		cfg:       config.Defaults(),
		costs:     cost.Global,
		days:      days,
		jsonOut:   jsonOut,
		out:       out,
		readState: func() (*state.State, error) { return seededState(), nil },
	}
}

func TestForecastDefaultDays(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, 0, false) // 0 -> defaults to 30
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "30") {
		t.Fatalf("expected 30-day horizon:\n%s", out.String())
	}
}

func TestForecastJSON(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, 90, true)
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Days       int     `json:"days"`
		DailyBurn  float64 `json:"dailyBurn"`
		HorizonCost float64 `json:"horizonCost"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out.String())
	}
	if payload.Days != 90 || payload.DailyBurn <= 0 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/cost/forecast/`
Expected: FAIL — package/`command` undefined.

- [ ] **Step 3: Implement `forecast.go`**

```go
// Package forecast implements "fabrica cost forecast": project the current
// monthly cost estimate over a time horizon (daily burn, horizon cost,
// annualized). Offline — same config-derived model as cost report.
package forecast

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/costsource"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

const defaultDays = 30

const caveat = "Note: estimates reflect current fabrica.yaml; run `<module> status` to reconcile."

type command struct {
	cfg       *config.Config
	costs     *fabricacost.Registry
	days      int
	jsonOut   bool
	out       io.Writer
	readState func() (*fabricastate.State, error)
}

// New returns the "cost forecast" subcommand.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var days int
	cmd := &cobra.Command{
		Use:   "forecast",
		Short: "Project the current monthly estimate over a time horizon",
		Long: `Project the current estimated monthly cost over a time horizon: daily burn
rate, total cost over the horizon, and annualized cost. Offline — uses the same
config-derived estimate as cost report.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			c := command{
				cfg:       rt.Config,
				costs:     fabricacost.Global,
				days:      days,
				jsonOut:   opts.JSONOutput,
				out:       out,
				readState: func() (*fabricastate.State, error) { return provision.ReadState(rt) },
			}
			return c.run()
		},
	}
	cmd.Flags().IntVar(&days, "days", defaultDays, "forecast horizon in days")
	return cmd
}

func (c command) run() error {
	days := c.days
	if days <= 0 {
		days = defaultDays
	}
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	b := costsource.Aggregate(c.cfg, st, c.costs)
	f := fabricacost.Project(b.Total, days, b.Confidence)
	if c.jsonOut {
		return c.renderJSON(f)
	}
	f.Render(c.out)
	fmt.Fprintln(c.out, caveat)
	return nil
}

func (c command) renderJSON(f fabricacost.Forecast) error {
	payload := struct {
		MonthlyEstimate float64 `json:"monthlyEstimate"`
		Days            int     `json:"days"`
		DailyBurn       float64 `json:"dailyBurn"`
		HorizonCost     float64 `json:"horizonCost"`
		Annualized      float64 `json:"annualized"`
		Confidence      string  `json:"confidence"`
		Note            string  `json:"note"`
	}{
		MonthlyEstimate: f.MonthlyEstimate,
		Days:            f.Days,
		DailyBurn:       f.DailyBurn,
		HorizonCost:     f.HorizonCost,
		Annualized:      f.Annualized,
		Confidence:      f.Confidence.String(),
		Note:            caveat,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	fmt.Fprintln(c.out, string(data))
	return nil
}
```

- [ ] **Step 4: Wire into the parent**

In `cmd/cost/cost.go`, add the import `"github.com/jpvelasco/fabrica/cmd/cost/forecast"` and the line `cmd.AddCommand(forecast.New(runtimeSource, optionsSource, out))` after the report line.

- [ ] **Step 5: Run tests**

Run: `go test ./cmd/cost/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/cost/forecast/ cmd/cost/cost.go
git commit -m "feat(cost): cost forecast subcommand"
```

---

### Task 8: `cost alerts` (list / set / check)

**Files:**
- Create: `cmd/cost/alerts/alerts.go` (parent + all three sub-subcommands in one package)
- Modify: `cmd/cost/cost.go` (wire the child)
- Test: `cmd/cost/alerts/alerts_test.go`

**Interfaces:**
- Consumes: `globals.*`, `provision.ReadState`, `costsource.{Aggregate,MapBudgets}`, `cost.{EvaluateBudgets,RenderBudgets}`, `config.Config.Save`, `config.BudgetThreshold`.
- Produces: `alerts.New(runtimeSource, optionsSource, out) *cobra.Command` (parent wiring `list`/`set`/`check`).

Design: all three live in package `alerts` as separate `command` structs (`listCommand`, `setCommand`, `checkCommand`) to keep seams distinct. `set` has a `cfgSave func(*config.Config, string) error` seam; `set`/`check` reuse the `readState` seam.

- [ ] **Step 1: Write the failing test**

Create `cmd/cost/alerts/alerts_test.go`:

```go
package alerts

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/state"
)

func seededState() *state.State {
	st := state.NewState("acct", "us-east-1")
	st.Modules = []state.ModuleState{{
		Name: "perforce", Status: "ready",
		Resources: []state.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-1"},
			{TypeName: "AWS::EC2::Volume", Identifier: "vol-1"},
		},
	}}
	return st
}

func TestListText(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Defaults()
	cfg.Cost.Budgets = []config.BudgetThreshold{{Scope: "total", Monthly: 400, WarnPct: 80}}
	c := listCommand{cfg: cfg, out: &out}
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "total") || !strings.Contains(out.String(), "400") {
		t.Fatalf("missing threshold:\n%s", out.String())
	}
}

func TestSetUpsertsAndSaves(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Defaults()
	var saved *config.Config
	c := setCommand{
		cfg:     cfg,
		out:     &out,
		cfgPath: "fabrica.yaml",
		cfgSave: func(cc *config.Config, path string) error { saved = cc; return nil },
	}
	if err := c.run("perforce", 150, 0); err != nil {
		t.Fatal(err)
	}
	if saved == nil || len(saved.Cost.Budgets) != 1 {
		t.Fatalf("expected one saved budget, got %+v", saved)
	}
	if saved.Cost.Budgets[0].Scope != "perforce" || saved.Cost.Budgets[0].Monthly != 150 {
		t.Fatalf("unexpected budget: %+v", saved.Cost.Budgets[0])
	}
	// upsert: setting the same scope again replaces, does not append.
	if err := c.run("perforce", 200, 90); err != nil {
		t.Fatal(err)
	}
	if len(saved.Cost.Budgets) != 1 || saved.Cost.Budgets[0].Monthly != 200 || saved.Cost.Budgets[0].WarnPct != 90 {
		t.Fatalf("upsert failed: %+v", saved.Cost.Budgets)
	}
}

func TestSetDryRunWritesNothing(t *testing.T) {
	var out bytes.Buffer
	saveCalled := false
	c := setCommand{
		cfg:     config.Defaults(),
		out:     &out,
		dryRun:  true,
		cfgPath: "fabrica.yaml",
		cfgSave: func(*config.Config, string) error { saveCalled = true; return nil },
	}
	if err := c.run("total", 500, 0); err != nil {
		t.Fatal(err)
	}
	if saveCalled {
		t.Fatal("dry-run must not write config")
	}
	if !strings.Contains(out.String(), "500") {
		t.Fatalf("dry-run should print the change:\n%s", out.String())
	}
}

func TestSetValidation(t *testing.T) {
	c := setCommand{cfg: config.Defaults(), out: &bytes.Buffer{}, cfgSave: func(*config.Config, string) error { return nil }}
	if err := c.run("perforce", 0, 0); err == nil {
		t.Fatal("expected error for monthly <= 0")
	}
	if err := c.run("nonsense", 100, 0); err == nil {
		t.Fatal("expected error for unknown scope")
	}
}

func TestCheckEvaluates(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Defaults()
	cfg.Cost.Budgets = []config.BudgetThreshold{{Scope: "perforce", Monthly: 10}} // way under -> OVER
	c := checkCommand{
		cfg:       cfg,
		costs:     cost.Global,
		out:       &out,
		readState: func() (*state.State, error) { return seededState(), nil },
	}
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "OVER") {
		t.Fatalf("expected OVER:\n%s", out.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/cost/alerts/`
Expected: FAIL — package/types undefined.

- [ ] **Step 3: Implement `alerts.go`**

```go
// Package alerts implements "fabrica cost alerts": manage and evaluate local
// budget thresholds. list/check are read-only; set upserts a threshold into
// fabrica.yaml (honoring --dry-run). Thresholds are local guardrails only — no
// AWS Budgets resources are created.
package alerts

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/costsource"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

// knownScopes are the valid budget scopes: "total" plus each module name.
var knownScopes = map[string]bool{
	"total": true, "perforce": true, "horde": true,
	"workstation": true, "ci": true, "deploy": true,
}

// New returns the "cost alerts" parent command.
func New(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alerts",
		Short: "Manage and check local budget thresholds",
		Long: `Manage local budget thresholds and check the current estimate against them.
Thresholds are local guardrails written to fabrica.yaml — no AWS Budgets
resources are created. cost alerts check is informational (exit code stays 0).`,
	}
	cmd.AddCommand(newList(runtimeSource, optionsSource, out))
	cmd.AddCommand(newSet(runtimeSource, optionsSource, out))
	cmd.AddCommand(newCheck(runtimeSource, optionsSource, out))
	return cmd
}

// ---- list ----

type listCommand struct {
	cfg     *config.Config
	jsonOut bool
	out     io.Writer
}

func newList(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show configured budget thresholds",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			c := listCommand{cfg: rt.Config, jsonOut: optionsSource().JSONOutput, out: out}
			return c.run()
		},
	}
}

func (c listCommand) run() error {
	budgets := c.cfg.Cost.Budgets
	if c.jsonOut {
		data, err := json.MarshalIndent(budgets, "", "  ")
		if err != nil {
			return fmt.Errorf("encoding JSON: %w", err)
		}
		fmt.Fprintln(c.out, string(data))
		return nil
	}
	if len(budgets) == 0 {
		fmt.Fprintln(c.out, "No budget thresholds configured. Set one with: fabrica cost alerts set <scope> <monthly>")
		return nil
	}
	fmt.Fprintln(c.out, "Configured budget thresholds:")
	for _, b := range budgets {
		warn := b.WarnPct
		if warn <= 0 {
			warn = 80
		}
		fmt.Fprintf(c.out, "  %-12s $%-9.2f (warn at %d%%)\n", b.Scope, b.Monthly, warn)
	}
	return nil
}

// ---- set ----

type setCommand struct {
	cfg     *config.Config
	dryRun  bool
	out     io.Writer
	cfgPath string
	cfgSave func(*config.Config, string) error
}

func newSet(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	var warnPct int
	cmd := &cobra.Command{
		Use:   "set <scope> <monthly>",
		Short: "Configure a budget threshold (writes fabrica.yaml)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			opts := optionsSource()
			monthly, err := strconv.ParseFloat(args[1], 64)
			if err != nil {
				return fmt.Errorf("invalid monthly amount %q: must be a number (USD/month)", args[1])
			}
			c := setCommand{
				cfg:     rt.Config,
				dryRun:  opts.DryRun,
				out:     out,
				cfgPath: rt.ConfigFile(),
				cfgSave: func(cc *config.Config, path string) error { return cc.Save(path) },
			}
			return c.run(args[0], monthly, warnPct)
		},
	}
	cmd.Flags().IntVar(&warnPct, "warn-pct", 0, "warn threshold as percent of monthly (0 = default 80)")
	return cmd
}

func (c setCommand) run(scope string, monthly float64, warnPct int) error {
	if monthly <= 0 {
		return fmt.Errorf("monthly budget must be greater than 0 (got %v) — pass a positive USD amount", monthly)
	}
	if !knownScopes[scope] {
		return fmt.Errorf("unknown scope %q — must be \"total\" or a module name (perforce, horde, workstation, ci, deploy)", scope)
	}
	// Upsert into a copy so dry-run never mutates shared config.
	updated := upsert(c.cfg.Cost.Budgets, config.BudgetThreshold{Scope: scope, Monthly: monthly, WarnPct: warnPct})

	if c.dryRun {
		fmt.Fprintf(c.out, "Would set budget: %s = $%.2f", scope, monthly)
		if warnPct > 0 {
			fmt.Fprintf(c.out, " (warn at %d%%)", warnPct)
		}
		fmt.Fprintln(c.out)
		fmt.Fprintln(c.out, "Dry run — fabrica.yaml not modified.")
		return nil
	}
	c.cfg.Cost.Budgets = updated
	if err := c.cfgSave(c.cfg, c.cfgPath); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	fmt.Fprintf(c.out, "Set budget: %s = $%.2f\n", scope, monthly)
	return nil
}

// upsert replaces the threshold for a scope if present, else appends it.
func upsert(budgets []config.BudgetThreshold, b config.BudgetThreshold) []config.BudgetThreshold {
	out := make([]config.BudgetThreshold, len(budgets))
	copy(out, budgets)
	for i := range out {
		if out[i].Scope == b.Scope {
			out[i] = b
			return out
		}
	}
	return append(out, b)
}

// ---- check ----

type checkCommand struct {
	cfg       *config.Config
	costs     *fabricacost.Registry
	jsonOut   bool
	out       io.Writer
	readState func() (*fabricastate.State, error)
}

func newCheck(runtimeSource globals.RuntimeSource, optionsSource globals.OptionsSource, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "check",
		Short: "Evaluate the current estimate against configured thresholds",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := runtimeSource()
			if err != nil {
				return err
			}
			c := checkCommand{
				cfg:       rt.Config,
				costs:     fabricacost.Global,
				jsonOut:   optionsSource().JSONOutput,
				out:       out,
				readState: func() (*fabricastate.State, error) { return provision.ReadState(rt) },
			}
			return c.run()
		},
	}
}

func (c checkCommand) run() error {
	st, err := c.readState()
	if err != nil {
		return fmt.Errorf("reading state: %w", err)
	}
	b := costsource.Aggregate(c.cfg, st, c.costs)
	statuses := fabricacost.EvaluateBudgets(b.PerScope, costsource.MapBudgets(c.cfg.Cost.Budgets))
	// Deterministic order for stable output.
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Scope < statuses[j].Scope })
	if c.jsonOut {
		return c.renderJSON(statuses)
	}
	if len(statuses) == 0 {
		fmt.Fprintln(c.out, "No budget thresholds configured. Set one with: fabrica cost alerts set <scope> <monthly>")
		return nil
	}
	fabricacost.RenderBudgets(c.out, statuses)
	return nil
}

func (c checkCommand) renderJSON(statuses []fabricacost.BudgetStatus) error {
	type jsonStatus struct {
		Scope     string  `json:"scope"`
		Estimate  float64 `json:"estimate"`
		Threshold float64 `json:"threshold"`
		WarnPct   int     `json:"warnPct"`
		State     string  `json:"state"`
		NoMatch   bool    `json:"noMatch"`
	}
	out := make([]jsonStatus, 0, len(statuses))
	for _, s := range statuses {
		out = append(out, jsonStatus{
			Scope: s.Scope, Estimate: s.Estimate, Threshold: s.Threshold,
			WarnPct: s.WarnPct, State: s.State.String(), NoMatch: s.NoMatch,
		})
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding JSON: %w", err)
	}
	fmt.Fprintln(c.out, string(data))
	return nil
}
```

- [ ] **Step 4: Wire into the parent**

In `cmd/cost/cost.go`, add the import `"github.com/jpvelasco/fabrica/cmd/cost/alerts"` and `cmd.AddCommand(alerts.New(runtimeSource, optionsSource, out))` after the forecast line.

- [ ] **Step 5: Run tests**

Run: `go test ./cmd/cost/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/cost/alerts/ cmd/cost/cost.go
git commit -m "feat(cost): cost alerts list/set/check subcommands"
```

---

### Task 9: Register in root + docs

**Files:**
- Modify: `cmd/root/root.go`
- Modify: `ROADMAP.md`, `CLAUDE.md`, `fabrica.example.yaml`
- Test: existing root/cobra tests (no new test needed; the wiring is exercised by `cmd/cost/cobra_test.go`).

**Interfaces:**
- Consumes: `cost.New` (Task 6).
- Produces: `fabrica cost ...` reachable from the root command.

- [ ] **Step 1: Register the command in root**

In `cmd/root/root.go`:

1. Add the import `"github.com/jpvelasco/fabrica/cmd/cost"` (in the existing import group, alphabetical among the `cmd/*` imports).
2. After the `cmd.AddCommand(deploy.New(runtimeSource, optionsSource, out))` line, add:

```go
	cmd.AddCommand(cost.New(runtimeSource, optionsSource, out))
```

- [ ] **Step 2: Verify it builds and is reachable**

Run: `go build ./... && go run . cost report --help`
Expected: build clean; help text for `cost report` prints.

Run: `go run . cost --help`
Expected: lists `report`, `forecast`, `alerts` subcommands.

- [ ] **Step 3: Add the example to `fabrica.example.yaml`**

Append a commented cost section (match the file's existing comment style — inspect it first):

```yaml
# Local budget guardrails (cost alerts). Purely informational — no AWS Budgets
# resources are created. Scope is "total" or a module name.
# cost:
#   budgets:
#     - scope: total
#       monthly: 400
#       warnPct: 80
#     - scope: perforce
#       monthly: 150
```

- [ ] **Step 4: Update `ROADMAP.md`**

1. Milestone 4 checkboxes → checked:

```
- ✅ `fabrica cost report`/`forecast`/`alerts`
- ✅ Multi-module reporting and budget guardrails
```

2. Module-status table `cost` row → `✅ Complete — offline config-derived report/forecast + local budget alerts`.
3. Add the follow-up line under Milestone 4 (or the Phase 2+ list): `⬜ Backfill ModuleResource.Properties with cost Name at create time (read cost inputs from state, not config).`

- [ ] **Step 5: Update `CLAUDE.md`**

1. Package tables — add rows:
   - `cmd/cost` — parent; wires report/forecast/alerts. No live provider.
   - `cmd/cost/report` — offline monthly breakdown by module; `--json`.
   - `cmd/cost/forecast` — projects the estimate over `--days` (default 30); `--json`.
   - `cmd/cost/alerts` — list/set/check local budget thresholds; `set` honors `--dry-run`.
   - `cmd/internal/costsource` — shared `Aggregate` engine; sole owner of module enumeration for cost.
   - `internal/cost` additions — `Project`/`Forecast`, `EvaluateBudgets`/`BudgetStatus`, render helpers.
2. Update the "Project Status" paragraph: cost module implemented; Milestone 4 done.
3. Add a "Cost-Specific Notes" section:
   - Config-derive model: report reflects current `fabrica.yaml`, scoped to modules in state — fully offline, no live provider.
   - Stopped instances drop the compute line; EBS still billed.
   - Deploy fleet cost counted only when a `AWS::GameLift::Fleet` exists in state.
   - Local thresholds only — no AWS Budgets / SNS; `alerts check` is informational (exit 0).
   - Follow-up: `ModuleResource.Properties` cost backfill (deferred).
4. Update the "Planned Command Structure" tree: mark `cost` line `✓ implemented`.

- [ ] **Step 6: Run the full suite + lint**

Run: `go build ./... && go test ./... && go vet ./... && gofmt -l .`
Expected: build clean, all tests pass, vet clean, `gofmt -l` prints nothing.
Run: `golangci-lint run ./...` — Expected: zero warnings.
Run: `go list -deps ./internal/cloud/... | grep -E 'internal/(state|cost)' || echo "OK: cloud does not import state/cost"` — Expected: OK line.

- [ ] **Step 7: Commit**

```bash
git add cmd/root/root.go ROADMAP.md CLAUDE.md fabrica.example.yaml
git commit -m "feat(cost): register cost command + docs (Milestone 4)"
```

- [ ] **Step 8: Final verification before PR**

Run the module end-to-end against the local state cache:

```bash
go run . cost report
go run . cost forecast --days 90
go run . cost alerts set total 500
go run . cost alerts list
go run . cost alerts check
go run . --json cost report
```

Expected: report shows per-module breakdown + total + caveat; forecast shows 90-day projection; `alerts set` writes `fabrica.yaml`; `alerts list` shows the threshold; `alerts check` shows OK/WARN/OVER; `--json` emits valid JSON. Confirm `alerts set total 500` actually added a `cost.budgets` entry to `fabrica.yaml` (then revert that local edit if it is not meant to be committed).

## Plan complete

All spec sections are covered:
- Forecast projection + budget evaluation → Tasks 2, 3.
- Per-module `CostResources` extraction → Task 4.
- `costsource` engine (stopped-drop, deploy-fleet-gate, unknown-module) → Task 5.
- `cmd/cost` report/forecast/alerts + `--json`/`--dry-run` → Tasks 6, 7, 8.
- Typed `CostConfig` → Task 1.
- Docs/roadmap/example → Task 9.
- Out-of-scope items (Cost Explorer, AWS Budgets, Properties backfill, non-zero exit) are respected — none implemented; the Properties backfill is recorded as a tracked follow-up.
