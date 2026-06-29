# Cost Management Module — Design (Phase 1, Milestone 4)

Status: approved for implementation
Date: 2026-06-28

## Goal

Implement the `fabrica cost` command family: clear, actionable visibility and
local guardrails on estimated infrastructure cost across every provisioned
module (Perforce, Horde, Workstation, CI, Deploy).

```
fabrica cost report                          # current estimated monthly breakdown by module/resource
fabrica cost forecast [--days 30]            # project the estimate over a time horizon
fabrica cost alerts list                     # show configured budget thresholds
fabrica cost alerts set <scope> <monthly>    # configure a budget threshold (writes fabrica.yaml)
fabrica cost alerts check                     # evaluate current estimate against thresholds (OK/WARN/OVER)
```

All three subcommands support `--json`. `cost alerts set` honors `--dry-run`
(prints the change, writes nothing).

## Central constraint (drives the whole design)

Cost estimators key off `cost.Resource{TypeName, Name}`, where `Name` carries
the cost-relevant detail the estimator parses:

- `AWS::EC2::Instance` → `Name` is the instance type (`m5.xlarge`)
- `AWS::EC2::Volume` → `Name` is `gp3-<n>GiB`
- `AWS::GameLift::Fleet` → `Name` is `<instanceType>x<count>` (`fleetCostName`)

But **state (`.fabrica/state.json`) records only `{TypeName, Identifier}`** —
the AWS resource ID (`i-…`, `sg-…`), never the cost-relevant `Name`.
`ModuleResource.Properties` is currently unused.

### Decision: hybrid — config-derive now, Properties backfill later

`cost report`/`forecast` derive cost inputs from the **current `fabrica.yaml`**,
scoped to **which modules are present in state**. The module-name list comes
from state (what's actually provisioned); the cost detail comes from config
(what those modules are configured as).

Consequence: the report is **fully offline** — it reads the local state cache
the same way `fabrica status` does, and needs **no live cloud provider**.

Documented caveat in every report: *"Estimates reflect current fabrica.yaml.
If config changed since provisioning, run the relevant `<module> status` to
reconcile."*

Tracked follow-up (NOT this milestone): backfill `ModuleResource.Properties`
with the cost `Name` at create time so a future version can read cost inputs
straight from state for maximum accuracy. Captured in ROADMAP under Milestone 4.

## Architecture

Dependency flow is unchanged: `cmd/cost → internal/{config, state, cost, cmd-internal}`.
The cost engine touches no AWS SDK and no live provider.

### 1. `internal/cost` additions (pure; no AWS, no config import)

`internal/cost` stays provider-agnostic and dependency-free. New pure types and
functions, each unit-tested in isolation:

**Forecast projection**

```go
type Forecast struct {
    MonthlyEstimate float64
    Days            int
    DailyBurn       float64 // MonthlyEstimate / daysPerMonth
    HorizonCost     float64 // DailyBurn * Days
    Annualized      float64 // MonthlyEstimate * 12
    Confidence      ConfidenceLevel
}

func Project(monthly float64, days int, conf ConfidenceLevel) Forecast
```

`daysPerMonth = 30.44` (avg). Deterministic — no clock seam needed. `days <= 0`
defaults to 30 at the command layer, not here (here it's validated `> 0`).

**Budget evaluation**

```go
type BudgetThreshold struct {
    Scope   string  // "total" or a module name ("perforce", "horde", …)
    Monthly float64 // threshold in USD/month
    WarnPct int     // warn when estimate >= this % of Monthly; 0 → default 80
}

type BudgetState int // OK, Warn, Over

type BudgetStatus struct {
    Scope     string
    Estimate  float64
    Threshold float64
    WarnPct   int
    State     BudgetState
}

func EvaluateBudgets(perScope map[string]float64, thresholds []BudgetThreshold) []BudgetStatus
```

`perScope` carries each module's subtotal plus a `"total"` key. Evaluation is
pure: `Over` when `estimate >= threshold`, `Warn` when `estimate >= threshold*WarnPct/100`,
else `OK`. A threshold whose scope has no estimate evaluates against 0 (`OK`)
and is flagged in render as "(no matching resources)".

**Render helpers** (mirror the existing `Report.Render(io.Writer, width)` style,
`fmt.Fprintf` tables): `Forecast.Render`, and a budget-status table renderer.
Text only; JSON is assembled at the command layer from the typed structs.

### 2. Per-module `CostResources(cfg)` extraction (small, justified refactor)

Each module's `NewCreatePlan` currently builds `CostResources` inline from
config fields + defaults. That logic — and *only* that logic — is what the cost
engine needs, but the constructors take `ctx`/`VPCResolver`/extra args and have
validation side effects, so calling them from a read-only cost path is wrong.

Extract a pure, AWS-free helper in each `internal/<module>` package:

```go
// internal/perforce/cost.go (and horde, workstation, ci, deploy equivalents)
func CostResources(cfg config.PerforceConfig) []cost.Resource
```

`NewCreatePlan` is refactored to call this same helper (single source of truth
for "what does this module cost-wise look like at config X"). Default values
(e.g. `m5.xlarge`, `500 GiB`) live in the helper. No behavior change to create
commands — verified by existing create tests.

Deploy is the nuance: its standing monthly cost is the **active fleet**
(`fleetCostName(instanceType, desiredInstances)`) — IAM role and alias are ~free
and excluded from the standing-cost helper. `deploy.CostResources(cfg)` returns
the fleet line; the engine includes it only when a fleet resource exists in state.

### 3. `cmd/internal/costsource` (new shared engine)

Single owner of "enumerate provisioned modules → estimated cost breakdown."
This is genuine shared substance across all three subcommands (report, forecast,
alerts all need the same aggregate), matching the `modstatus`/`teardown` pattern.

```go
type ModuleCost struct {
    Name       string
    Status     string
    Report     cost.Report   // per-resource detail from reg.EstimateAll
    Subtotal   float64
}

type Breakdown struct {
    Modules    []ModuleCost
    Total      float64
    Confidence cost.ConfidenceLevel
    PerScope   map[string]float64 // module subtotals + "total"
}

func Aggregate(cfg *config.Config, st *state.State, reg *cost.Registry) Breakdown
```

Logic:

1. For each `ModuleState` in `st.Modules`, switch on `m.Name` to pick the right
   `CostResources(cfg.<Module>)` helper. **This switch is the only place that
   enumerates modules** (like `status` owns its enumeration).
2. **Stopped instances**: when `m.Status == "stopped"`, drop the
   `AWS::EC2::Instance` line (compute not billed); keep `AWS::EC2::Volume`
   (EBS still billed). Annotate the module's report note.
3. **Deploy**: include the fleet cost line only when state actually contains a
   `AWS::GameLift::Fleet` resource (a setup-only deploy module with just
   role+alias has ~zero standing cost).
4. `reg.EstimateAll(resources)` per module → `cost.Report`; sum to subtotal;
   roll confidence up to the least-confident module.
5. Build `PerScope` for budget evaluation.

Unknown module names in state (forward-compat) contribute zero with a "no
estimator wired" note rather than erroring.

### 4. `cmd/cost/` (parent + subcommands)

Mirrors `cmd/deploy` / `cmd/ci`: a parent `New()` that wires subcommands, each
taking `RuntimeSource` + `OptionsSource` closures. **None require a live
provider** — they use `rt.Config` + local state only. `readState` is a seam
field (same `ReadStateOrNew` path as `status`).

- **`cmd/cost`** — parent command, wires the three children.
- **`cmd/cost/report`** — `costsource.Aggregate` → per-module table (resource
  lines, subtotals), grand total, confidence, caveat line; `--json` emits the
  `Breakdown`.
- **`cmd/cost/forecast`** — `--days` (default 30); `cost.Project(breakdown.Total,
  days, breakdown.Confidence)` → daily burn / horizon cost / annualized table;
  `--json` emits `Forecast`.
- **`cmd/cost/alerts`** — parent with three sub-subcommands:
  - `list` — print thresholds from `cfg.Cost.Budgets`; `--json`.
  - `set <scope> <monthly> [--warn-pct N]` — upsert a threshold into config and
    `cfg.Save(rt.ConfigFile())`. `--dry-run` prints the change and writes
    nothing. Validates `monthly > 0`, scope ∈ {`total`, known module names}.
    `cfgSave` is a seam field.
  - `check` — `Aggregate` + `EvaluateBudgets` → OK/WARN/OVER table; `--json`.
    Exit code stays 0 (report tool, not a gate) — V1 keeps it informational;
    a future `--strict` could return non-zero.

### 5. `internal/config` — typed cost section

Replace `Cost any` with:

```go
type CostConfig struct {
    Budgets []BudgetThreshold `mapstructure:"budgets" yaml:"budgets"`
}
type BudgetThreshold struct {
    Scope   string  `mapstructure:"scope"   yaml:"scope"`
    Monthly float64 `mapstructure:"monthly" yaml:"monthly"`
    WarnPct int     `mapstructure:"warnPct" yaml:"warnPct,omitempty"`
}
```

Wire through `Config`, `fileConfig`, and `fileConfig()`. To avoid `internal/cost`
↔ `internal/config` coupling, `config.BudgetThreshold` is its own type;
`costsource` maps `[]config.BudgetThreshold` → `[]cost.BudgetThreshold` at the
boundary (same pattern as config carrying plain types the plan layer consumes).
`emptySection` handling is removed for cost (now a concrete struct, marshals
cleanly as `budgets: []`).

## Output sketches

```
$ fabrica cost report
Cost estimate (monthly) — based on current fabrica.yaml
----------------------------------------------------------------
  perforce        (ready)
    AWS::EC2::Instance   m5.xlarge       $140.16   high
    AWS::EC2::Volume     gp3-500GiB       $40.00   high
    subtotal                             $180.16
  horde           (ready)
    …                                    $ 187.32
----------------------------------------------------------------
  Total:                                 $367.48
Confidence: high
Note: estimates reflect current fabrica.yaml; run `<module> status` to reconcile.

$ fabrica cost forecast --days 30
Cost forecast (30 days) — based on current monthly estimate $367.48
  Daily burn:        $12.07
  30-day cost:       $362.16
  Annualized:      $4,409.76
Confidence: high

$ fabrica cost alerts check
Budget check (warn at 80% of threshold)
  total      $367.48 / $400.00   [WARN]   (92% of budget)
  perforce   $180.16 / $150.00   [OVER]
  horde      $187.32 / $250.00   [OK]     (75% of budget)
```

## Testing

- **`internal/cost`**: unit tests for `Project` (rounding, days≤0 guard, zero
  monthly) and `EvaluateBudgets` (OK/Warn/Over boundaries, missing-scope,
  default WarnPct).
- **`cmd/internal/costsource`**: `Aggregate` over a synthetic `State` + `Config`
  — multi-module totals, stopped-instance compute drop, deploy fleet-present vs
  setup-only, unknown module name.
- **Each `cmd/cost/*` package**: two-file pattern —
  - white-box `*_test.go`: call `command.run()` with injected `readState` /
    `cfgSave` seams; cover dry-run, threshold upsert, validation errors, JSON.
  - black-box `cobra_test.go`: build a minimal root replicating the
    `--json`/`--dry-run` persistent-flag hierarchy; assert command wiring.
- **Module `CostResources` helpers**: assert the extracted helper returns the
  same `[]cost.Resource` the create tests already expect (refactor safety).
- **Integration (gated/manual)**: cost commands provision nothing, so the
  "real AWS + teardown" rule doesn't naturally apply. A documented manual check
  runs `cost report` against an already-provisioned stack and confirms it reads
  S3-backed state correctly. No throwaway resources created solely to test a
  read-only command. (Approved deviation from the integration-test convention
  for this read-only module.)

Coverage target: 60%+ for new `internal/*` and `cmd/internal/*` code.

## Out of scope (V1 — documented)

- AWS Cost Explorer / billing actuals (deferred to Vigiles per ROADMAP).
- AWS `Budgets::Budget` resources / SNS email alerts (local thresholds only).
- `ModuleResource.Properties` cost backfill (tracked Milestone 4 follow-up).
- Non-zero exit on `alerts check` (informational in V1; `--strict` is future).

## Docs / roadmap updates on completion

- ROADMAP Milestone 4 → ✅; module-status table `cost` row → ✅; add the
  Properties-backfill follow-up line.
- CLAUDE.md: add `cmd/cost`, `internal/cost` additions, `cmd/internal/costsource`
  to the package tables + a "Cost-Specific Notes" section (config-derive model,
  offline, stopped-instance handling, local-thresholds-only).
- `fabrica.example.yaml`: add a commented `cost.budgets` example.
```
