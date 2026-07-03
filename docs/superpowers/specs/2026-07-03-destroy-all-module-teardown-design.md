# `destroy --all` Module Teardown — Design (Phase 1, Milestone 5, Sub-project A)

Status: awaiting user review
Date: 2026-07-03

## Goal

Make `fabrica destroy --all` tear down **every provisioned module** (perforce,
horde, workstation, ci, deploy) and then the state backend — a true full-stack
teardown. Today `destroy --all` deletes only the Phase 0 state backend (S3 +
DynamoDB) and leaves all module resources (EC2 instances, GameLift fleets,
CodeBuild projects, IAM roles) running and billing. That is the central gap
this sub-project closes.

## Decisions (proceeding on these; flagged for veto at review)

The user was away when these came up. Each is the safety-first choice matching
the codebase's established philosophy. **Veto any at the review gate.**

1. **Scope:** `--all` tears down all modules in reverse dependency order, then
   the state backend. The backend is deleted **only if every module teardown
   succeeded**.
2. **CI included:** `--all` removes CI resources (CodeBuild project + IAM role),
   closing the V1 gap. This requires a real `ci destroy` subcommand (see below).
3. **Confirmation:** a single aggregate typed phrase (`destroy all <account>`)
   after the full plan is shown — not one phrase per module.
4. **On failure:** continue tearing down remaining modules, collect errors, and
   never delete the backend if anything failed. Report what remains + retry.

## Central constraint (drives the design)

The existing `teardown.Command` engine (`cmd/internal/teardown`) runs one
module: read state → plan → confirm → delete resources in order (via a
`ResourceOrder` hook) → persist state after each deletion. Its only deletion
seam is `DeleteResource(ctx, *cloud.Resource)` — a **Cloud Control** call.

- **perforce, horde, workstation, deploy** are entirely Cloud Control resources
  (EC2 instance/SG; GameLift fleet/build/alias + IAM role). They already tear
  down through `teardown.Command` today (perforce/horde/workstation `destroy`,
  and `deploy destroy`).
- **CI is the exception.** `AWS::CodeBuild::Project` has no Cloud Control CREATE
  and is created/deleted via the `cloud.CodeBuildRunner` SDK auxiliary interface
  (`DeleteProject`). Its IAM role *is* Cloud Control. So CI cannot flow through
  the generic engine unmodified — the engine can't delete the CodeBuild project.

### CI teardown resolution

Add a **`ci destroy`** subcommand (`cmd/ci/destroy`) that:
1. Calls `CodeBuildRunner.DeleteProject` for the project (SDK; missing project is
   not an error — already idempotent).
2. Deletes the IAM role via Cloud Control (`DeleteResource`).
3. Persists state after each step (same recoverable-partial-failure pattern),
   then removes the module.

`ci destroy` is standalone-useful (fills the documented V1 gap) AND the
orchestrator reuses it. This is better than burying CI-specific SDK logic inside
the `destroy --all` orchestrator. It does NOT use `teardown.Command` (the engine
assumes a single Cloud Control deletion seam); it is a small purpose-built
command following the same seam/confirm/state-persist conventions.

## Architecture

New shared engine: **`cmd/internal/destroyall`** — orchestrates the full
teardown. `cmd/destroy` delegates to it when `--all` is set.

```
cmd/destroy (--all) → cmd/internal/destroyall.Run
                          ├─ per module (reverse dep order): teardown.Command.Run (with a "child" mode)
                          ├─ ci: ci/destroy teardown func (SDK + Cloud Control)
                          └─ state backend: existing StateBackendDestroyer path
```

### Teardown order

Reverse of provisioning / dependency order:

```
deploy → ci → workstation → horde → perforce → [state backend]
```

Rationale: deploy/ci/workstation are leaf consumers; perforce (source control)
is the foundational service others reference, so it goes last. The backend is
deleted last of all, and only on full success.

**Deploy `--all` semantics inside the orchestrator:** `deploy destroy` has its
own `--all` flag meaning "also remove the alias + IAM role" (by default it keeps
them so a game backend's alias survives). Since `fabrica destroy --all` wipes the
entire stack, the orchestrator must invoke deploy's teardown with deploy-level
`all=true` (via its `resourceOrder(all)` / `spec(all)` builders) so the alias and
role are removed too. A full destroy that left the deploy alias + role behind
would be an incomplete wipe.

### `destroyall.Run` flow

1. Read state. If no provider → "No infrastructure found." If no modules AND no
   backend → nothing to do.
2. Resolve account/region via `Provider.Identity`.
3. Build the aggregate plan: for each provisioned module (in teardown order),
   the resources that would be deleted; plus the backend (bucket + table).
4. `--dry-run`: print the whole plan (per-module resource lists + backend +
   deletion order) and return. No AWS calls.
5. Print the plan. Take confirmation:
   - `--yes`: skip prompt, print the standard "proceeding without confirmation"
     warning.
   - else: require the single phrase `destroy all <account>` via
     `prompt.ConfirmExact`. Wrong phrase → cancel, no AWS calls.
6. Execute teardown in order. For each module, invoke its teardown with
   confirmation already satisfied (a "child"/`skipConfirm` path — see below) and
   `--json` suppressed at the child level (the orchestrator owns output).
   - Collect per-module outcome: destroyed IDs, or error.
   - On a module error: record it, continue to the next module (decision #4).
7. After all modules: if **any** module failed, print a summary of what remains
   and how to retry (`fabrica <module> destroy`), and **do not** touch the
   backend. Exit with an error.
8. If all modules succeeded, delete the backend via `StateBackendDestroyer`
   (reusing the existing `destroyBackend` logic from `cmd/destroy`).
9. Print aggregate completion; `--json` emits the aggregate result.

### Reusing `teardown.Command` without double-confirmation

`teardown.Command.Run` currently owns its own confirm + plan print. For the
orchestrated path we need it to skip its own confirmation (the aggregate phrase
already covered it) and let the orchestrator drive output.

Add a field to `teardown.Command`:

```go
// SkipConfirm, when true, bypasses the interactive confirmation and the
// standalone plan/confirmation output — used when an orchestrator (destroy
// --all) has already confirmed the aggregate operation.
SkipConfirm bool
```

When `SkipConfirm` is true, `Run` proceeds straight to `apply` after the
not-provisioned check and dry-run check, and suppresses the standalone plan
header (the orchestrator printed the aggregate plan). This is a minimal,
well-scoped addition — the alternative (extracting `apply` into a public method)
leaks more surface. `SkipConfirm` defaults false, so every existing caller is
unchanged.

`ci destroy` gets the same treatment via its own `skipConfirm` field.

### Aggregate output shape (`--json`)

```go
type ModuleResult struct {
    Module    string   `json:"module"`
    Destroyed []string `json:"destroyed"`
    Error     string   `json:"error,omitempty"`
}
type Result struct {
    Modules        []ModuleResult `json:"modules"`
    BackendDeleted bool           `json:"backendDeleted"`
    DryRun         bool           `json:"dryRun"`
}
```

## Components / files

**New:**
- `cmd/internal/destroyall/destroyall.go` — the orchestration engine: plan
  build, aggregate confirm, ordered module teardown, failure handling, backend
  delete, text + JSON output.
- `cmd/internal/destroyall/destroyall_test.go` — white-box: multi-module order,
  dry-run plan, aggregate confirm accept/reject, one-module-fails-skips-backend,
  all-succeed-deletes-backend, empty state, `--json`.
- `cmd/ci/destroy/destroy.go` — `ci destroy` subcommand (SDK project delete +
  Cloud Control role delete; `skipConfirm` seam).
- `cmd/ci/destroy/destroy_test.go` + `cmd/ci/destroy` cobra coverage.

**Modified:**
- `cmd/internal/teardown/teardown.go` — add `SkipConfirm bool`; honor it in
  `Run` (skip confirm + standalone plan when set). No behavior change when false.
- `cmd/destroy/destroy.go` — when `--all`, delegate module+backend teardown to
  `destroyall.Run`. The existing backend-only logic moves into (or is called by)
  the orchestrator so there is one code path. Plain `destroy` usage hint stays.
- `cmd/ci/ci.go` — wire the new `destroy` subcommand.
- Docs: `ROADMAP.md` (destroy --all row → done for modules; note CI destroy),
  `CLAUDE.md` (destroy --all + ci destroy behavior, the `SkipConfirm` seam,
  teardown order), command tree.

## Error handling

- Module teardown errors are collected, not fatal mid-run (decision #4). Each is
  reported against its module with the retry command.
- The backend is a hard gate: any module failure ⇒ backend preserved ⇒ state
  still tracks orphaned resources for a clean retry.
- Transitional EC2 states (stopping/shutting-down) already error inside
  `teardown.Command` — that surfaces as that module's error and the orchestrator
  continues, leaving the backend intact.
- A backend deletion failure (e.g. non-empty bucket) reuses the existing
  `ErrStateBucketNotEmpty` messaging.

## Testing

- **`cmd/internal/destroyall`**: white-box with injected seams (fake
  readState/writeState, fake per-module teardown outcomes, fake backend
  destroyer). Cover: teardown order, dry-run (no delete calls), aggregate
  confirm accept + reject, one module fails → remaining still attempted + backend
  untouched + error returned, all succeed → backend deleted, empty state, JSON.
- **`cmd/ci/destroy`**: two-file pattern — white-box (`run()` with fake
  `CodeBuildRunner` + fake Cloud Control delete + state seams; project-missing is
  not an error; role delete ordering; state persisted) and black-box cobra
  (flag hierarchy, wiring).
- **`cmd/internal/teardown`**: add a test that `SkipConfirm=true` bypasses the
  confirm seam and still deletes + persists (guards the new branch).
- **`cmd/destroy`**: extend existing tests for the `--all` delegation (dry-run
  aggregate plan; confirm gate) using injected orchestrator seams.
- Coverage target: 60%+ for new `cmd/internal/*` and `cmd/*` code.

## Out of scope (this sub-project)

- The other M5 sub-projects (E2E testing, docs refresh, consistency audit,
  release prep) — each is its own spec.
- `perforce backup`/`restore` (separately tracked).
- Selective teardown (`destroy --module perforce`) — the per-module `destroy`
  commands already cover that; `--all` is the aggregate.
- Parallel teardown — modules are torn down sequentially in dependency order for
  predictable output and safe ordering.

## Docs / roadmap updates on completion

- ROADMAP: `destroy --all` row → ✅ (modules + backend); `ci` row note that
  `ci destroy` now exists.
- CLAUDE.md: destroy --all orchestration + teardown order; `ci destroy`; the
  `teardown.SkipConfirm` seam; `cmd/internal/destroyall` in the package tables.
