# Milestone 2: CI Module — Design

**Date:** 2026-06-28
**Branch:** `feat/milestone-2-ci`
**Status:** Approved decisions baked in (see below); implementing under "you have the helm" authorization.

## Goal

A `fabrica ci` command family that provisions and manages build pipelines as an
**orchestration layer over Horde**. CodeBuild (V1) runs the orchestration entry
point; Horde remains the BuildGraph executor. CodePipeline orchestration is
deferred to a later milestone.

## Approved Decisions (from brainstorming)

| # | Decision | Choice |
|---|----------|--------|
| 1 | CI's role vs Horde | **Orchestration layer over Horde** — CodeBuild conducts; Horde executes BuildGraph jobs. |
| 2 | `ci trigger <buildgraph>` semantics | **Start a pipeline execution.** In V1 (no CodePipeline yet) this means starting the **CodeBuild project**; the command surface stays stable when CodePipeline lands later. |
| 3 | V1 `ci setup` scope | **CodeBuild project + IAM role only.** CodePipeline + source wiring deferred. |

### Reconciliation note (decision 2 vs 3)

Decision 2 said `ci trigger` triggers a *CodePipeline execution*, but decision 3
defers CodePipeline. There is no pipeline to execute in V1, so `ci trigger`
starts the **CodeBuild project** — the orchestration entry point that exists in
V1. User-facing semantics ("trigger a build run") are identical; when
CodePipeline is added, `trigger` starts the pipeline that wraps CodeBuild
without changing the command surface or flags.

## Relationship to existing `horde submit`

`horde submit <buildgraph>` already parses a BuildGraph file and POSTs it
directly to Horde's REST API (fire-and-forget or `--wait`). That stays as the
**low-level, direct-to-Horde** path. `ci trigger` is the **orchestrated** path:
it starts a CodeBuild build whose generated buildspec submits the BuildGraph job
to Horde (resolving Horde's private IP from state). Two layers, clearly
separated; no code deleted from `horde submit`.

## Architecture

Follows the canonical Fabrica module pattern (`internal/perforce`, `internal/horde`):

```
cmd/ci/{setup,trigger,status,logs}  → internal/ci (pure plan layer)
                                    → rt.Provider.Resources()   (Cloud Control: IAM + CodeBuild CRUD)
                                    → rt.Provider.(cloud.CodeBuildRunner)  (runtime: StartBuild/status/logs)
```

### `internal/ci` — pure plan layer (no AWS SDK imports)

- `CreatePlan` struct + `NewCreatePlan(cfg config.CIConfig, account, region, hordeAddr string)`.
- Cloud Control desired-state builders:
  - `RoleDesiredState(plan)` → `AWS::IAM::Role` JSON (trust policy for `codebuild.amazonaws.com`, inline policy: CloudWatch Logs + read Horde/Perforce instance metadata; least-privilege).
  - `ProjectDesiredState(plan, roleARN)` → `AWS::CodeBuild::Project` JSON (Linux container, `BUILD_GENERAL1_SMALL`, env vars `HORDE_URL`/`FABRICA_REGION`, `NO_SOURCE` source with inline buildspec).
- `Buildspec(plan)` / `BuildspecRaw(plan)` — generated buildspec YAML that, on build, submits the BuildGraph job to Horde (`curl` to `$HORDE_URL/api/v1/jobs`, mirroring `hordeHTTPClient`). `*Raw` variant returns the plain string for test assertions (per the `GenerateRaw` convention).
- Cost estimators: register `AWS::CodeBuild::Project` and `AWS::IAM::Role` (IAM = $0) via `cost.Global.Register`. **Do not** re-register EC2/EBS/S3/DynamoDB.

### `cloud.CodeBuildRunner` — auxiliary interface (runtime ops)

Cloud Control does CRUD but not runtime build operations, exactly like EC2
stop/start. Define in `internal/cloud/codebuild.go`:

```go
type CodeBuildRunner interface {
    StartBuild(ctx context.Context, project string, env map[string]string) (buildID string, err error)
    BuildStatus(ctx context.Context, buildID string) (BuildInfo, error)
    BuildLog(ctx context.Context, buildID string) (string, error)
}

type BuildInfo struct {
    ID, Status, Phase string
    LogGroup, LogStream string
}
```

Implemented by `awsProvider` in `internal/cloud/aws/codebuild.go` (seam-injected
SDK clients, mirroring `state_backend.go` / `ec2manager.go`). Reached via type
assertion `rt.Provider.(cloud.CodeBuildRunner)`.

### `cmd/ci` — Cobra commands (seam-based, two-file tests)

- **`ci setup`** — provision IAM role → CodeBuild project (ordered, state written
  after each). Dry-run (plan + cost), y/N confirm (`--yes` skips), idempotent
  (existing resources detected and skipped). Mirrors `perforce/horde create` +
  `setup` confirmation.
- **`ci trigger <buildgraph>`** — read CI state for the project; resolve Horde
  private IP from Horde module state (like `horde submit`); parse the BuildGraph
  file (reuse `internal/horde/buildgraph`); `StartBuild` with env overrides
  `BUILDGRAPH`/`TARGET`/`HORDE_URL`. `--wait` polls `BuildStatus` to terminal.
- **`ci status`** — show CI infra (project, role) from state + live build status
  via `BuildStatus` for the most recent build; `--json`.
- **`ci logs <build-id>`** — fetch CloudWatch logs for a build via `BuildLog`.

### Config — `internal/config/config.go`

Replace the `CI any` field with a typed `CIConfig`:

```go
type CIConfig struct {
    ProjectName  string `mapstructure:"projectName"  yaml:"projectName"`
    ComputeType  string `mapstructure:"computeType"  yaml:"computeType"`  // default BUILD_GENERAL1_SMALL
    Image        string `mapstructure:"image"        yaml:"image"`        // default aws/codebuild/amazonlinux2-x86_64-standard:5.0
    BuildTimeout int    `mapstructure:"buildTimeout" yaml:"buildTimeout"` // minutes, default 60
}
```

Defaults applied in the plan layer (same pattern as Horde). Keeps `mapstructure`
tags; lives in `config.go` to avoid circular imports.

### State

New module name `"ci"`. Resources tracked: `AWS::IAM::Role`, `AWS::CodeBuild::Project`.
`UpsertModule("ci", project.Name, status, resources)`. Teardown of CI is **not**
in this milestone (no `ci destroy`); `destroy --all` remains state-backend-only.
Out-of-scope note documented.

## Integration with Perforce & Horde

- **Horde:** `ci trigger` resolves the Horde coordinator's private IP from the
  `horde` module's state + Cloud Control (same logic as `horde submit`), injects
  it as `HORDE_URL`; the buildspec submits the job to Horde. Requires Horde
  provisioned — clear error if not.
- **Perforce:** the CodeBuild IAM role is granted read access to describe the
  Perforce instance (for future P4 sync in builds); `HORDE_URL` is the V1 wiring.
  Buildspec includes a commented P4 sync placeholder (documented, not active) to
  avoid scope creep while signposting the integration point.

## Testing

- **Unit (hermetic, default + CI):** plan-layer tests (desired-state JSON shape,
  buildspec content, defaults, cost); `cmd/ci/*` white-box with seams (setup
  confirm/dry-run/idempotent/partial-failure; trigger resolves Horde + starts
  build + wait; status; logs) + black-box `cobra_test.go` per command.
  `CodeBuildRunner` AWS impl tested with faked SDK clients (mirroring
  `state_backend_test.go`). Target ≥60%.
- **Integration (opt-in):** `//go:build integration` + `FABRICA_INTEGRATION=1` +
  account guard. Creates IAM role + CodeBuild project, asserts existence, then
  `t.Cleanup` deletes both unconditionally. (No build *execution* in the
  integration test — that needs a live Horde; documented.)

## Docs

- `ROADMAP.md` — mark Milestone 2 / `ci` (`setup`,`trigger`,`status`,`logs`) implemented.
- `CLAUDE.md` — add `cmd/ci` + `internal/ci` rows; note `CodeBuildRunner` alongside
  the other auxiliary interfaces; CI config section.
- `README.md` — `fabrica ci` command docs + a short pipeline example.
- `fabrica.example.yaml` — add a `ci:` section.

## Out of scope (later Phase 1 / Phase 2)

- CodePipeline orchestration + source (CodeStar/S3) wiring.
- `ci destroy` / CI teardown in `destroy --all`.
- Active Perforce sync inside builds (placeholder only).
- Multi-pipeline / multi-project management.
