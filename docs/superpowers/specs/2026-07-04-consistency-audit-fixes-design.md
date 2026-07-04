# Consistency-Audit Fixes — Design (Phase 1, Milestone 5, Sub-project D)

Status: approved for implementation
Date: 2026-07-04

## Context

Milestone 5-D was a read-only consistency audit run as a 4-way parallel sweep
(layering, style/errors, tests/dead-code, docs). **Architecture came back clean**
— all 14 layering checks passed (dependency flow, plan-layer SDK isolation,
config centralization, cost-registration, auxiliary-interface pattern, no
cycles). No structural changes needed.

The audit surfaced doc/convention/test findings. Per decision, this sub-project
fixes **docs + cleanup only**; test-coverage gaps and cosmetic items are
explicitly deferred.

## Scope (approved: "docs + cleanup only")

### 1. AGENTS.md — de-stale (Important; actively misleading)

AGENTS.md is 26 days old and pre-dates the `setup` implementation + later
milestones. Fix the actively-wrong claims:
- Line ~23: **"`fabrica setup` is not yet functional … does not create any AWS
  resources"** — FALSE; setup shipped (S3 + DynamoDB bootstrap, PR #58). Replace
  with the current reality (setup is fully functional, idempotent, confirmed).
- Remove the reference to a nonexistent `docs/setup-manual.md`.
- Line ~93: **"Coverage target: 60%+ for `internal/*`"** → the real gate is
  Codecov **patch ≥90%** (see `codecov.yml`, CLAUDE.md). Correct it.
- The module list is frozen at Phase 0 (perforce/horde/workstation only). Rather
  than duplicate the full module table (which drifts), point readers to
  ROADMAP.md + CLAUDE.md for current module status, and note Phase 1 core is
  complete. Keep AGENTS.md as a higher-level orientation doc, not a status
  mirror.

### 2. README.md — fix the `m7i` instance-type error (Important; cost/sizing)

README says the Horde default is `m7i.xlarge` in two places (line ~141 prose,
line ~337 config example), but the code defaults to **`m7i.2xlarge`**
(`internal/horde/plan.go:43`, `internal/horde/cost.go:15`). Correct both to
`m7i.2xlarge`. (This is exactly the semantic-accuracy error the doc-drift guard
cannot catch — it checks command *presence*, not flag/value accuracy. Expected;
documented limitation.)

### 3. CLAUDE.md — fix the "No sentinel errors" rule (Important; doc-vs-code)

CLAUDE.md's Conventions say **"No sentinel errors"**, but `internal/cloud`
intentionally defines and uses two:
- `ErrResourceNotFound` (`internal/cloud/provider.go`)
- `ErrStateBucketNotEmpty` (`internal/cloud/state_backend.go`)

Both are used correctly with `errors.Is` (teardown idempotency, non-empty-bucket
detection). The **code is right; the rule is wrong.** Amend the rule to allow
narrow, package-level sentinels in `internal/cloud` for conditions callers must
branch on via `errors.Is`, while keeping the "prefer wrapped `%w` context
errors; no ad-hoc sentinels in cmd/module layers" spirit.

### 4. Delete the orphaned .NET project (cleanup; release hygiene)

`tests/Fabrica.Tests/` is an abandoned C#/xUnit project (`.csproj` + a stub
`UnitTest1.cs` + build artifacts under `bin/`/`obj/`) referencing a
`src/Fabrica.*` tree that does not exist. Fabrica is pure Go. It has no place in
a v0.1 release. Delete the whole directory. Cascades:
- Remove the `- "tests/Fabrica.Tests"` line from `codecov.yml`'s `ignore:` list
  (the path will no longer exist).
- Remove the now-obsolete **"## Orphaned .NET Test Project (Ignore)"** section
  from CLAUDE.md (nothing to ignore once it's gone).

### 5. ROADMAP.md — mark the audit done

Check the Milestone 5 line **"Final architecture + consistency review"** — this
audit IS that review. Note it found clean architecture. Leave the release-prep
line (E) unchecked.

## Out of scope (deferred, per decision)

- **Test-coverage gaps** (audit finding): missing `cobra_test.go` on 7 cmd
  packages, missing white-box tests on 2, AWS provider seams at 0%. Real and
  on-theme, but a separate, larger effort — deferred to its own sub-project.
- **Output-writer inconsistency** (`cmd/version` uses `cmd.OutOrStdout()`, others
  use the `c.out` seam) — cosmetic; not touched.
- **Anonymous multi-letter receivers** (`(renderer)`) — cosmetic; not touched.
- Any architecture/layering change — none needed (audit clean).

## Verification

- `go build ./... && go test ./... && go vet ./... && gofmt -l .` clean;
  `golangci-lint run ./...` 0 issues (deleting the .NET dir doesn't touch Go).
- Confirm `tests/Fabrica.Tests/` is gone and no Go/CI/go.mod reference remains
  (only `codecov.yml` referenced it — that line is removed).
- The doc-drift guard (`cmd/root/docs_drift_test.go`) still passes (README edits
  are value-only, don't remove command mentions).
- Re-read AGENTS.md / README / CLAUDE.md after edits for internal consistency
  (no dangling references to deleted content).

## Docs / roadmap updates on completion

- ROADMAP Milestone 5 "Final architecture + consistency review" → ✅ (clean
  architecture; docs/cleanup fixes applied; test-coverage gaps tracked as a
  follow-up).
