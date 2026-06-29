# Milestone 3 — Deploy Module Design

**Date:** 2026-06-28
**Status:** Approved (brainstorming complete; ready for implementation plan)
**Roadmap:** Phase 1, Milestone 3 (`fabrica deploy`)

## Goal

Implement a `fabrica deploy` command family that orchestrates GameLift
deployment of UE5 dedicated-server builds produced by the CI/Horde pipeline.
Fabrica owns the **build-to-deploy orchestration** path; live runtime fleet
operations (advanced scaling policies, FlexMatch matchmaking, session
management, deep monitoring) are explicitly out of scope and left to **Classis**.

## Scope decision (the product-boundary fork)

The product spec assigns GameLift fleet *runtime* ops to Classis; the ROADMAP
puts `fabrica deploy` (GameLift orchestration + blue/green) in Fabrica. These
are reconciled as:

- **In Fabrica (this milestone):** `deploy setup`, `deploy promote`,
  `deploy rollback`, `deploy status`, `deploy destroy`. Build registration,
  fleet creation tied to a build, alias-based blue/green, rollback, teardown.
- **Left to Classis (future):** live scaling policies, matchmaking
  configuration, game-session creation/management, deep runtime monitoring.

## Locked decisions

| Decision | Choice |
|----------|--------|
| Deploy scope | Orchestration in Fabrica; runtime ops to Classis |
| Fleet compute model | GameLift **managed EC2 fleets** (`ComputeType: EC2`) |
| Build source | **Register from S3 location** (CI/Horde uploads the packaged build; promote registers `AWS::GameLift::Build` referencing the S3 key + IAM role) |
| Blue/green strategy | **Alias flip, new fleet per promote**; old fleet retained for rollback (`SIMPLE` routing) |
| Fleet activation execution | **Cloud Control create (non-blocking) + SDK poller** (`cloud.GameLiftManager`) — Option A |
| Rollback surface | Dedicated **`deploy rollback`** subcommand (not a flag on promote) |
| Alias/role lifecycle | Long-lived setup infra; retained by default `destroy`, removed only by `destroy --all` |

## Why Option A (the central technical decision)

GameLift `Build`, `Fleet`, and `Alias` are modern CloudFormation registry
types, so they **do** support Cloud Control resource operations (unlike
`AWS::CodeBuild::Project`, which the `ci` module reaches via an SDK runner). So
resource CRUD stays on `rt.Provider.Resources()`, honoring the locked
"Cloud Control for IaC" decision.

**However**, the GameLift Fleet resource handler *stabilizes on `ACTIVE`* — the
same behavior CloudFormation uses, where `CREATE_COMPLETE` is not reported until
the fleet finishes the `NEW → DOWNLOADING → VALIDATING → BUILDING → ACTIVATING →
ACTIVE` lifecycle (20–40 minutes, and can fail mid-activation). The existing
blocking `ResourceClient.Create()` calls `waiter.WaitForOutput` (default 15-min
timeout) and surfaces failures only as an opaque `progressEventError` — it
cannot show phase progress and swallows the `DescribeFleetEvents` detail that
explains *why* a fleet failed.

Option A therefore:

1. Creates Build and Alias via the normal **blocking** Cloud Control `Create()`
   (they stabilize in seconds).
2. Creates the Fleet via a **non-blocking** Cloud Control create seam: fire
   `CreateResource`, poll `GetResourceRequestStatus` only until the `Identifier`
   (FleetId) is populated (a few seconds, while still `NEW`/`IN_PROGRESS`),
   return the FleetId.
3. Polls fleet activation in the `cmd/deploy` layer via a new read-only SDK
   auxiliary interface `cloud.GameLiftManager` (`FleetStatus` + `FleetEvents`),
   printing phase transitions and surfacing real failure events — the same
   `--wait` + poll UX as `horde submit` / `ci trigger`.

This isolates only the *runtime status/events* concern (which Cloud Control
genuinely cannot express) behind a small SDK interface, exactly mirroring the
`CodeBuildRunner` and `EC2InstanceManager` splits already in the codebase.

### Non-blocking create seam

The blocking `ResourceClient.Create()` other modules rely on is **not changed**.
The non-blocking behavior is added as a separate path used only for the fleet —
e.g. a `CreateResourceAsync(ctx, r) (requestToken string, err error)` style
method on the AWS Cloud Control client that returns once `Identifier` is known,
or a dedicated fleet-create method. Build and Alias keep using blocking
`Create()`. Exact method shape is an implementation detail for the plan; the
contract is: *fire create, return FleetId quickly, do not block to ACTIVE.*

## Module shape

```
internal/deploy/              # pure plan layer, no AWS SDK imports
  plan.go                     # CreatePlan, PromotePlan, defaults, TypeName consts
  resources.go                # Cloud Control desired-state: BuildDesiredState,
                              #   FleetDesiredState, AliasDesiredState, alias-flip patch
  cost.go                     # GameLift fleet (instance-hours) + Build/Alias (free) estimators
  *_test.go
cmd/deploy/
  deploy.go                   # parent command; wires subcommands
  cobra_test.go
  setup/                      # IAM role + alias
  promote/                    # build -> fleet (non-blocking) -> poll -> alias flip
  rollback/                   # flip alias back to most-recent superseded fleet
  status/                     # fleet health, alias target, rollback candidates, events
  destroy/                    # teardown engine (default: fleets+builds; --all: + alias+role)
internal/cloud/
  gamelift.go                 # NEW: cloud.GameLiftManager interface + FleetInfo/FleetEvent
internal/cloud/aws/
  gamelift.go                 # NEW: SDK impl (FleetStatus, FleetEvents); awsProvider methods
  gamelift_test.go            # mocked GameLift SDK client
```

### Resources & execution path

| Resource | Type name | Path | Created by |
|----------|-----------|------|------------|
| IAM role (GameLift→S3 read) | `AWS::IAM::Role` | Cloud Control `Create` (blocking) | `setup` |
| Alias (`SIMPLE` routing) | `AWS::GameLift::Alias` | Cloud Control `Create` (setup) + `Update` (flip) | `setup`, flipped by `promote`/`rollback` |
| Build registration | `AWS::GameLift::Build` | Cloud Control `Create` (blocking) | `promote` |
| Fleet | `AWS::GameLift::Fleet` | Cloud Control create (**non-blocking**) + **SDK poll** | `promote` |

### New auxiliary interface (only new SDK surface)

```go
// internal/cloud/gamelift.go
type GameLiftManager interface {
    FleetStatus(ctx context.Context, fleetID string) (FleetInfo, error)
    FleetEvents(ctx context.Context, fleetID string) ([]FleetEvent, error)
}
type FleetInfo struct {
    FleetID string
    Status  string // NEW, DOWNLOADING, VALIDATING, BUILDING, ACTIVATING, ACTIVE, ERROR, ...
}
type FleetEvent struct {
    Code    string
    Message string
    Time    string
}
```

Reached via `rt.Provider.(cloud.GameLiftManager)`. `awsProvider` gains the
methods (delegating to a `gameLiftManager` like `ec2Mgr`/`CodeBuildRunner`).
The interface is **read-only** — all mutations go through Cloud Control.

## Command flows

### `deploy setup`

Idempotent; `--dry-run`, cost preview, y/N confirm (`--yes` skips). Structure
mirrors `ci setup`.

1. `AWS::IAM::Role` via Cloud Control — trust `gamelift.amazonaws.com`; inline
   policy `s3:GetObject` on the build bucket (GameLift pulls the build from S3).
2. `AWS::GameLift::Alias` via Cloud Control (blocking, fast) — `SIMPLE` routing.
   Until the first `promote`, the alias uses a `TerminalRoutingStrategy`
   (`MESSAGE` type) so it is valid before any fleet exists.
3. State stores role ARN + alias ID. Existing resources detected and skipped.

### `deploy promote <build-version>`

Seams: `readState`, `writeState`, `createResource`, `createFleetAsync`,
`updateResource`, `getResource`, `fleetStatus`, `fleetEvents`, `confirm`,
`sleep`, `now`.

1. **Resolve inputs** — read state for alias ID + role ARN (error → "run
   `fabrica deploy setup`"). S3 build location from `--s3-bucket`/`--s3-key`,
   defaulting to a convention (`builds/<build-version>/` in the configured/state
   bucket). `<build-version>` labels the build + fleet.
2. **Register build** — `AWS::GameLift::Build` via blocking Cloud Control
   `Create` (`StorageLocation` = bucket/key/role ARN; `OperatingSystem`,
   `Version`). Write state immediately.
3. **Cost preview + confirm** — estimate new fleet monthly cost; show plan
   (new fleet, instance type, build version, "old fleet retained for
   rollback"); y/N confirm (`--yes` skips; `--dry-run` stops here).
4. **Create fleet (non-blocking)** — `FleetDesiredState` (build ID, EC2 instance
   type, `FleetType` ON_DEMAND/SPOT, `RuntimeConfiguration` launch path,
   `EC2InboundPermissions`). Fire via the non-blocking seam → FleetId. Write
   state (`provisioning`, new fleet recorded) immediately.
5. **Poll activation** — `--wait` (default **on** for promote) polls
   `fleetStatus` ~every 20s up to `deploy.activationTimeoutMinutes` (default 45).
   Print phase transitions. On `ERROR`/timeout: pull `fleetEvents`, print them,
   **leave alias untouched** (old fleet still serving), exit non-zero.
   `--no-wait` returns after step 4 with a track hint.
6. **Flip alias** — once `ACTIVE`, `updateResource` (RFC-6902 patch) repoints
   `RoutingStrategy.FleetId` to the new fleet.
7. **Record rollback target** — new fleet → `active`; previously-active fleet →
   `superseded` (rollback candidate); alias target updated. Print success +
   the `fabrica deploy rollback` hint.

### `deploy rollback`

Thin, safe, no resource creation.

1. Read state; find alias + most-recent `superseded` fleet (error → "nothing to
   roll back to").
2. Verify that fleet is still `ACTIVE` via `fleetStatus` (could have been
   terminated out-of-band) — refuse with a clear message if not.
3. Show **current → target fleet** in the confirmation prompt; `prompt.Confirm`
   (simple y/N).
4. `updateResource` flips the alias back. Swap roles in state: target →
   `active`, just-demoted fleet → `superseded`. Print success.

### `deploy status`

Read-only; never mutates; `--json`.

- Alias ID + current routing target (live via `getResource`).
- **Active fleet:** live `fleetStatus` (+ phase if activating), build-version,
  instance type.
- **Retained/superseded fleets:** live status each, **clearly labeled
  "rollback candidate"**, with the `fabrica deploy rollback` hint.
- Active fleet `ERROR`/activating → surface recent `fleetEvents`.
- Setup-not-done / no-promote-yet → actionable next steps.

### `deploy destroy`

Uses the shared `teardown.Command` + `Spec` engine (reverse-order delete,
state-after-each, `--dry-run`, typed-phrase `ConfirmExact`, `--json`).

- **Default `deploy destroy`:** deletes **fleets + builds only**. Prints a short
  **warning that the alias + IAM role are preserved** (long-lived; game backends
  reference the alias). Order fleet → build (fleet references build) falls out of
  reverse-creation order.
- **`deploy destroy --all`:** also deletes alias + IAM role (symmetric with
  `setup`).

**Engine change:** add a nil-able `Spec.ResourceFilter func(ModuleResource) bool`
to the teardown engine. `deploy destroy` passes a filter (fleets+builds, or all
when `--all`); the other three callers (perforce destroy, horde destroy,
workstation terminate) pass `nil` and are unchanged. This keeps deploy on the
shared engine instead of forking it.

## State model

Module key `deploy` (existing `state.ModuleState` / `ModuleResource`; no new
types).

- `AWS::IAM::Role`, `AWS::GameLift::Alias` — long-lived (setup).
- `AWS::GameLift::Build`, `AWS::GameLift::Fleet` — one per promote.
- `ModuleResource.Properties` (existing `map[string]string` field on the type)
  carries:
  - `buildVersion` on build + fleet resources.
  - `role` on fleet resources: `active` (alias points here) | `superseded`
    (rollback candidate) | `draining`/`retired`.
- Module `version` = active build-version; module `status` =
  `provisioning`/`ready`/`error`.

## Cost estimation

`internal/deploy/cost.go` registers `AWS::GameLift::Fleet` with a fleet
estimator: instance-hours = desired-instances × hourly-rate × 730 (GameLift EC2
on-demand ≈ EC2 on-demand for the same family). `AWS::GameLift::Build` and
`AWS::GameLift::Alias` are free. **Does not** re-register `AWS::EC2::Instance`
(already in `internal/perforce/cost.go`). Confidence Low/Medium, noted.

## Error handling

- `fmt.Errorf("context: %w", err)` throughout; no sentinel errors.
- Every user-facing failure states the fix.
- Fleet `ERROR` → print `DescribeFleetEvents` + "inspect with
  `fabrica deploy status`".
- Alias flip fails *after* fleet `ACTIVE` → explicit: "new fleet is ACTIVE but
  alias still points at the old fleet; re-run `fabrica deploy promote` or flip
  manually" (no data loss; old fleet still serving).
- Activation failures always pull `DescribeFleetEvents` so the operator sees the
  real cause (bad launch path, build validation failure, capacity/quota).

## Testing

Two-file pattern per command package.

- **White-box `*_test.go`** (`package <cmd>`): inject all seams. Cover:
  - promote: build registered but fleet create fails → state recoverable;
    fleet `ERROR` → no alias flip; alias flip fails post-`ACTIVE` → clear error;
    `--dry-run` stops before any write; confirmation rejection.
  - rollback: no superseded fleet; target no longer `ACTIVE`; happy path.
  - status: rendering across all states (no setup, setup-only, active-only,
    active+superseded, error).
  - destroy: default (fleets+builds) vs `--all` (incl. alias+role) filtering.
- **Black-box `cobra_test.go`** (`package <cmd>_test`): minimal root replicating
  the persistent-flag hierarchy (`--dry-run`/`--yes`/`--json` on root). A single
  `cobraFakeProvider` implementing `Provider` + `GameLiftManager`.
- **`internal/deploy`** plan/resources/cost unit tests.
- **`internal/cloud/aws/gamelift_test.go`** with a mocked GameLift SDK client
  (no real AWS calls), matching `codebuild_test.go` style.
- Coverage target 60%+ for `internal/*`.

## Out of scope (V1)

- Scaling policies, FlexMatch matchmaking, game-session creation/management,
  deep runtime monitoring (→ Classis).
- Auto-draining of active game sessions on fleet delete (surface GameLift's own
  error with guidance instead).
- CodePipeline wiring; multi-region fleets; Realtime (script) fleets;
  GameLift Anywhere / container fleets.

## How this integrates with existing modules

CI builds (CodeBuild over Horde) produce the packaged UE5 dedicated-server
build and upload it to a known S3 location; `deploy promote <build-version>`
registers that S3 artifact as a GameLift Build and rolls it out. This completes
the `Perforce → Horde → CI → Deploy` chain end to end within Fabrica's
orchestration boundary.
