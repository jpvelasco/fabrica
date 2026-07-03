# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What Fabrica Is

A Go CLI + infrastructure-as-code framework that provisions and manages game studio cloud infrastructure on AWS: Perforce Helix Core, Unreal Horde build farms, CI/CD, GameLift deployment, and cloud workstations. Sister tool to [Ludus](https://github.com/jpvelasco/ludus) — while Ludus handles a single developer's pipeline, Fabrica scales that to a full studio.

## Project Status

[`ROADMAP.md`](ROADMAP.md) is the single source of truth for phases, module status, and the Praetorium vision. Update it when module status changes.

Phase 0 (CLI skeleton + AWS foundation) is complete; Phase 1 Milestones 1–4 are done. Implemented modules: `perforce` (create/status/destroy), `horde` (create/status/submit/destroy/ami), `workstation` (create/list/stop/start/terminate), `ci` (setup/trigger/status/logs — CodeBuild orchestration over Horde), `deploy` (setup/promote/rollback/status/destroy — GameLift blue/green orchestration), and `cost` (report/forecast/alerts — offline config-derived reporting + local budget alerts). All five `ResourceClient` methods in `internal/cloud/aws/cloudcontrol.go` are implemented against the real Cloud Control API — new modules can use `rt.Provider.Resources()` for resource types Cloud Control supports (verify first; see the CI notes for the CodeBuild exception).

`fabrica setup` is fully functional: `internal/state/bootstrap.go` type-asserts the provider to `cloud.StateBackendBootstrapper` and creates the S3 state bucket (versioning + encryption + public-access-block) and the DynamoDB lock table, idempotently. `cmd/setup/setup.go` shows the plan + cost estimate, then prompts for y/N confirmation before any AWS write (`--yes` skips; `--dry-run` shows the plan and cost without creating anything). `fabrica status` is the aggregate read-only overview: state-backend health + per-module status, resource counts, and actionable next steps, with `--probe` for opt-in TCP readiness checks (off by default because modules use private IPs); it never mutates state.

## Build Commands

```bash
go build ./...                         # requires Go 1.25.11+; defaults to Version=dev Commit=unknown
go build -ldflags "-X github.com/jpvelasco/fabrica/internal/version.Version=v1.0.0 -X github.com/jpvelasco/fabrica/internal/version.Commit=$(git rev-parse --short HEAD)" .  # release build
go vet ./...
go test ./...                          # Windows (no -race)
go test -race -v ./...                 # macOS
go test -race -coverprofile=coverage.out -covermode=atomic ./...  # Linux only
go test ./... -run TestName            # single test
golangci-lint run ./...
go tool cover -func=coverage.out       # coverage summary
gofmt -w .                             # format all Go files
```

CI runs lint + build + test cross-platform (ubuntu/windows/macos) on push/PR to main.

### Linting

`.golangci.yml` (v2 schema) starts from `default: none` and explicitly enables: `errcheck`, `govet`, `ineffassign`, `staticcheck`, `unused`, `gocritic`, `misspell`, `unconvert`, `gosec`, `dupl`. `gofmt` is the only formatter. `gosec` excludes G104/G301/G304/G306 (intentional best-effort cleanup, standard dir perms, config-file reads/writes) — match these rationales before adding new suppressions. Codacy mirrors this via `.codacy.yml` (govet + staticcheck engines); `.github/instructions/codacy.instructions.md` drives the Codacy MCP integration.

## Orphaned .NET Test Project (Ignore)

`tests/Fabrica.Tests/` is a C#/xUnit project (`Fabrica.Tests.csproj`) left over from an abandoned C# design. It references a `src/Fabrica.Cli`, `src/Fabrica.Constructs`, and `src/Fabrica.Operations` tree that **does not exist** — it will not build. Fabrica is pure Go. Do not add to it, fix it, or treat its references as real; the only test suite is the Go one (`go test ./...`).

## Git Hooks

Hooks live in `.githooks/` (tracked). Activate once per clone:

```bash
git config core.hooksPath .githooks
```

- **pre-commit**: runs `gofmt -l` and `go vet` on staged Go files
- **commit-msg**: enforces Conventional Commits (`feat|fix|refactor|test|docs|chore|perf|ci|build`)

## Architecture

### Dependency Flow

```
cmd/* → internal/{config, state, cost, tags, prompt, cloud}
                                                    ↓
                                        internal/cloud/aws
```

`internal/cloud/*` never imports `internal/state`, `internal/cost`, or any `cmd/*`. Verify after changes to `internal/`:

```bash
go list -deps ./internal/cloud/...
```

### Package Responsibilities

| Package | Purpose |
|---------|---------|
| `cmd/root` | Wires global flags (`--config`, `--verbose`, `--json`, `--dry-run`, `--yes`, `--profile`), initializes `globals.Store`, registers subcommands |
| `cmd/globals` | `Runtime` (Config + Provider + ConfigPath), `Options`, `Store.Init()`, dependency injection types |
| `cmd/{destroy,doctor,setup,status,configcmd,version}` | Subcommands; each `New()` accepts `RuntimeSource` + `OptionsSource` closures — no direct globals access |
| `cmd/status` | Aggregate read-only overview: state-backend health + per-module status, resource counts, and next steps; `--probe` opt-in TCP readiness checks; `--json`; never writes state |
| `internal/config` | `Config` struct, Viper loading from `fabrica.yaml` (scoped here only), YAML serialization, defaults |
| `internal/cloud` | Provider-agnostic interfaces: `Provider`, `ResourceClient`, `Resource`, `EC2InstanceManager`, `StateBackendChecker` (doctor/status: bucket/table exists checks), `StateBackendBootstrapper` (setup: create bucket/table), `StateBackendDestroyer` (destroy --all: delete bucket/table), `CodeBuildRunner` (ci: create/delete CodeBuild project + start/query builds — AWS::CodeBuild::Project has no Cloud Control CREATE) |
| `internal/cloud/aws` | AWS implementation registered via `init()` in `internal/cloud/registry.go`; wraps `cloudcontrol`, `s3`, `dynamodb`, `iam`, `ec2` SDK clients; `awsProvider` satisfies both `Provider` and `EC2InstanceManager` |
| `internal/state` | `State`/`ModuleState`/`ModuleResource` types, `Backend` interface, S3+DynamoDB bootstrap, DynamoDB locking |
| `internal/cost` | Cost estimator interface + Phase 0 estimators; registered by resource `TypeName` |
| `internal/tags` | Tag injection helpers; `ManagedBy: fabrica` applied to all resources |
| `internal/prompt` | `Confirm` (y/N) and `ConfirmExact` (typed phrase) for interactive confirmation dialogs |
| `internal/version` | Version constant |
| `cmd/perforce` | Parent command; wires create/status/destroy subcommands |
| `cmd/perforce/create` | Provision SG + EC2 instance in order; writes state after each; generates credentials |
| `cmd/perforce/status` | Reads state + Cloud Control live data; TCP-probes port 1666; transitions provisioning→ready |
| `cmd/perforce/destroy` | Deletes EC2 instance then SG in reverse order; skips already-terminated instances |
| `internal/perforce` | Pure plan layer (no SDK): version resolution, `CreatePlan`, Cloud Control JSON builders (`SGDesiredState`, `InstanceDesiredState`), cloud-init generator, cost estimators |
| `cmd/horde` | Parent command; wires create/status/submit/destroy/ami subcommands |
| `cmd/horde/create` | Provision SG + EC2 instance (AMI-first); generates MongoDB password; writes credentials to `.fabrica/horde-credentials.yaml` |
| `cmd/horde/status` | Reads state + Cloud Control live data; TCP-probes port 5000 (HTTP); transitions provisioning→ready; `--json` emits `hordeUrl`/`hordeGrpc` |
| `cmd/horde/submit` | Parses BuildGraph XML; resolves coordinator private IP via Cloud Control; POSTs to Horde REST API; supports `--wait` polling |
| `cmd/horde/destroy` | Deletes EC2 instance then SG in reverse order; skips already-terminated instances; mirrors perforce/destroy pattern |
| `cmd/horde/ami` | Local file generator — no AWS calls, no `RuntimeSource`. `build` subcommand renders embedded templates (`embed.FS`) to an output dir: EC2 Image Builder recipe JSON + optional Packer HCL + build-guide.md |
| `internal/horde` | Pure plan layer: `CreatePlan`, `SGDesiredState`, `InstanceDesiredState`, cloud-init generator (`Generate`/`GenerateRaw`), `VPCResolver` interface |
| `internal/horde/buildgraph` | Isolated sub-package: `ParseBuildGraph(path)` → `*BuildGraphJob`; XML-only, no AWS/HTTP deps |
| `cmd/workstation` | Parent command; wires create/list/stop/start/terminate subcommands |
| `cmd/workstation/create` | Provision SG + EC2 instance (AMI-first, NICE DCV); `--template artist\|programmer` sets instance type + volume presets; `--mount-perforce` injects p4 CLI + `~/.p4config` via cloud-init using Perforce private IP from local state; writes credentials to `.fabrica/workstation-credentials.yaml` |
| `cmd/workstation/list` | Reads local state; prints workstation status + resource IDs; `--json` emits `WorkstationEntry` array |
| `cmd/workstation/stop` | Calls `EC2InstanceManager.StopInstance`; updates state status to `"stopped"`; simple y/N confirmation (not typed phrase) |
| `cmd/workstation/start` | Calls `EC2InstanceManager.StartInstance`; updates state status to `"ready"`; no-ops when already running |
| `cmd/workstation/terminate` | Ordered delete (instance → SG) with incremental state cleanup; mirrors `perforce/destroy` exactly |
| `internal/workstation` | Pure plan layer: `CreatePlan` (accepts `tmpl` + `perforceAddr` args), `SGDesiredState`, `InstanceDesiredState`, cloud-init generator (`Generate`/`GenerateRaw`), `VPCResolver` interface. GPU instance prices (g4dn/g5/g6) live in `internal/perforce/cost.go` alongside the shared EC2 estimators. |
| `internal/credentials` | Shared helpers: `GeneratePassword`, `WriteCredentials` — write per-module credential YAML files to `.fabrica/` (mode 0600) |
| `internal/stateutil` | Shared helpers: `ResourceByType` — query module state without repeating the lookup loop |
| `cmd/ci` | Parent command; wires setup/trigger/status/logs subcommands |
| `cmd/ci/setup` | Provision IAM role (Cloud Control) + CodeBuild project (`CodeBuildRunner.EnsureProject`); dry-run, cost, y/N confirm, idempotent |
| `cmd/ci/trigger` | Parse BuildGraph (reuses `internal/horde/buildgraph`), resolve Horde private IP from state, `StartBuild` with `BUILDGRAPH`/`TARGET`/`HORDE_URL` env; `--wait` polls `BuildStatus` |
| `cmd/ci/status` | Reads CI module state (project + role); `--build <id>` shows live `BuildStatus`; `--json` |
| `cmd/ci/logs` | Fetches CloudWatch logs for a build via `CodeBuildRunner.BuildLog` |
| `internal/ci` | Pure plan layer: `CreatePlan`, `RoleDesiredState` (Cloud Control IAM), `ProjectSpec` (CodeBuild SDK), buildspec generator (`Buildspec`/`BuildspecRaw`), cost estimators (CodeBuild + IAM) |
| `cmd/deploy` | Parent command; wires setup/promote/rollback/status/destroy subcommands |
| `cmd/deploy/setup` | Provision IAM role (Cloud Control) + GameLift alias; idempotent; cost estimate + y/N confirm |
| `cmd/deploy/promote` | Register build from S3, create fleet (non-blocking via `CreateFleetAsync`), wait for ACTIVE, flip alias; reuses `cmd/internal/teardown` for fleet/build ordering |
| `cmd/deploy/rollback` | Flip alias back to most-recent retained fleet; instant operation |
| `cmd/deploy/status` | Show alias target + active fleet + rollback candidates with live fleet status; `--json` |
| `cmd/deploy/destroy` | Delete fleets + builds (default); `--all` also removes alias + role; reuses `cmd/internal/teardown` |
| `internal/deploy` | Pure plan layer: `CreatePlan`, `RoleDesiredState` + `BuildDesiredState` + `FleetDesiredState` + `AliasDesiredState` (Cloud Control), cost estimators (IAM + GameLift); `ResourceOrder` hook for teardown sequencing |
| `cmd/cost` | Parent command; wires report/forecast/alerts subcommands. No live provider — all operations are offline (config-derived + state-derived, no AWS calls) |
| `cmd/cost/report` | Estimated monthly cost broken down by module; reads local state + current `fabrica.yaml`; `--json` emits structured breakdown |
| `cmd/cost/forecast` | Projects the current estimate over a time horizon (`--days`, default 30); `--json` emits forecast entries (daily + summary) |
| `cmd/cost/alerts` | Manage and check local budget thresholds: `list` shows configured budgets, `set <scope> [--monthly N] [--warn-pct N]` updates `fabrica.yaml`, `check` evaluates current cost against thresholds and reports OK/WARN/OVER status |
| `cmd/internal/costsource` | Shared `Aggregate` engine; sole owner of module enumeration for cost (reads local state, applies stopped-instance drop, deploy fleet gate, unknown-module filtering); wired by report/forecast/alerts |
| `internal/cost` | `Project`/`Forecast` functions for time-series projection; `EvaluateBudgets`/`BudgetStatus` for threshold evaluation; render helpers (`RenderMonthly`, `RenderForecast`, `RenderBudgetStatus`) |

### Module Pattern

`internal/perforce` and `internal/horde` are the canonical templates for new modules:
- **Pure plan layer** — no AWS SDK imports. Builds `CreatePlan` and Cloud Control desired-state JSON. The `cmd/<module>` layer calls the plan layer, then executes via `rt.Provider.Resources()`.
- **VPCResolver interface** — when a module needs AWS-specific resolution (VPC, subnet), define an interface in `internal/<module>/config.go` that the provider implements. Keeps `internal/*` SDK-free.
- **EC2InstanceManager** — Cloud Control cannot stop or start EC2 instances. Use the `cloud.EC2InstanceManager` auxiliary interface (defined in `internal/cloud/ec2manager.go`, implemented by `awsProvider` in `internal/cloud/aws/`). Access via type assertion: `rt.Provider.(cloud.EC2InstanceManager)`. Follow the `state_backend.go` pattern when adding future provider-specific capabilities.
- **Verify Cloud Control support per resource type** — not every CloudFormation type supports the Cloud Control CREATE action. `AWS::CodeBuild::Project` returns `UnsupportedActionException`, so the `ci` module creates it through the `cloud.CodeBuildRunner` SDK auxiliary interface while the IAM role still goes through Cloud Control. When adding a new resource type, confirm support before assuming `rt.Provider.Resources().Create` works; if it doesn't, add an SDK-backed auxiliary interface (the `EC2InstanceManager` / `StateBackendBootstrapper` / `CodeBuildRunner` pattern).
- **Cost estimators** — m5/c5 (Perforce), m7i (Horde), and g4dn/g5/g6/c7i (workstation) EC2 prices live together in `internal/perforce/cost.go`. `cost.Global.Register` panics on duplicate `TypeName` — do not register `AWS::EC2::Instance` or `AWS::EC2::Volume` from a second package.
- **`GenerateRaw`** — when a function produces base64 output, add a `*Raw` variant returning the plain string for test assertions.
- **State written after each resource** — partial failures leave a recoverable record; re-running detects already-provisioned state and exits cleanly.
- **Config structs in `internal/config/config.go`** — `HordeConfig`, `PerforceConfig`, and `WorkstationConfig` all live here (not in their respective `internal/<module>` packages) to avoid circular imports.
- **Embedded templates** — file-generator commands (e.g. `cmd/horde/ami`) use `embed.FS` + `text/template` with `Option("missingkey=error")`. Templates live under `cmd/<cmd>/templates/`. No `RuntimeSource`/`OptionsSource` needed when a command makes no AWS calls.

### Shared command helpers (`cmd/internal/*`)

Cross-module command logic lives under `cmd/internal/` (importable only within `cmd/`). Extract here only when modules share *substance*, not merely *shape* — over-abstracting look-alike-but-different commands hurts readability more than the duplication costs.

- **`cmd/internal/teardown`** — full engine for the three teardown commands (`perforce destroy`, `horde destroy`, `workstation terminate`). They were byte-identical except presentational strings, so a `teardown.Command` + `teardown.Spec` (the varying strings) consolidates everything. Each command's `New()` is a thin `Spec` + wiring.
- **`cmd/internal/modstatus`** — orchestration engine for `perforce status` / `horde status`. The flow (read state → query EC2 → TCP-probe → transition → poll) is shared, but **rendering differs** (P4PORT vs hordeUrl), so each command implements a `modstatus.Renderer` over the engine's `Info` while the engine owns the flow.
- **`cmd/internal/provision`** — small helpers for the three `create` commands (`ReadState`, `ConfirmPhrase`, `PrintConfirmInstructions`). Create was deliberately **not** given a full engine: the `applyCreate` steps look parallel but each calls module-specific code (credentials, desired-state builders, plan types), so a generic engine would add indirection without removing real duplication. Only the genuinely-identical boilerplate was extracted; `applyCreate` and `print*` stay local to each command. See issue #37 for the rationale.

The rule of thumb across these three: **teardown shared everything (engine + Spec), status shared the spine but split rendering (engine + Renderer), create shared only leaf helpers.** Match the abstraction to how much is genuinely common.

### Provider Registration

`internal/cloud/aws/aws.go` registers the AWS provider via a blank-import side-effect (`_ "github.com/jpvelasco/fabrica/internal/cloud/aws"` in `cmd/root`). New providers follow the same `init()` pattern against `internal/cloud/registry.go`.

### Config + State

Config: `fabrica.yaml` (or `fabrica-<profile>.yaml` with `--profile`). Copy `fabrica.example.yaml` for a starting point. State: S3 bucket (`fabrica-state-<account-id>`) + DynamoDB table (`fabrica-state-lock`) remote, with `.fabrica/state.json` local cache.

## Architecture Decisions (Locked)

- **IaC:** AWS Cloud Control API (`aws-sdk-go-v2/service/cloudcontrol`) — no Terraform, no Pulumi, no external binaries
- **Module path:** `github.com/jpvelasco/fabrica`
- **Config:** Viper + YAML — Viper scoped inside `internal/config` only; no logging library, `fmt.Printf`/`Println` only

## Test Strategy

Every command package uses a two-file test approach (established in `cmd/perforce/` and `cmd/horde/`):

- `*_test.go` (`package <cmd>`) — white-box tests that call `command.run()` directly with injected seams (`readState`, `writeState`, `createResource`, `probeTCP`, `hordeClient`, `sleep`, `now`). Cover partial failures, confirmation rejection, error propagation.
- `cobra_test.go` (`package <cmd>_test`) — black-box Cobra-layer tests that call `cmd.New(...) + ExecuteContext`. Build a minimal root command in the test to replicate the persistent-flag hierarchy (`--dry-run`, `--yes`, `--json` live on root, not on the subcommand).

Seam pattern: the `command` struct holds `func` fields for all I/O operations. `New()` wires real implementations; tests inject fakes. `fakeProvider` / `fakeHordeClient` patterns live in `*_test.go` files alongside the tests that use them. When a command uses both `Provider` and `EC2InstanceManager` (e.g. workstation stop/start), cobra tests use a single `cobraFakeProvider` that implements both interfaces to avoid the type assertion in tests.

## How to Add a New Module

1. **`internal/<module>/`** — pure plan layer, no AWS SDK imports: `CreatePlan` struct, Cloud Control desired-state JSON builders, cloud-init generator, cost estimators.
2. **Confirm Cloud Control support** for each resource type the module provisions. If a type lacks a CREATE action (or needs runtime ops Cloud Control doesn't expose), add an SDK-backed auxiliary interface in `internal/cloud/` + `internal/cloud/aws/` and reach it via type assertion — don't force it through `Resources()`.
3. **`cmd/<module>/`** — Cobra command with `RuntimeSource` + `OptionsSource` closures; seam fields on the `command` struct for all I/O.
4. **Config struct** — add to `internal/config/config.go` (not `internal/<module>/`) with `mapstructure:` tags to avoid circular imports.
5. **Cost estimators** — register via `cost.Global.Register` in the plan layer. Do not re-register `AWS::EC2::Instance` or `AWS::EC2::Volume` — already registered in `internal/perforce/cost.go`.
6. **Wire** the parent command in `cmd/root/root.go`.
7. **Tests** — two-file pattern: white-box `*_test.go` + black-box `cobra_test.go` with a minimal root that replicates the persistent-flag hierarchy.

Reference: `cmd/perforce/` + `internal/perforce/` are the canonical templates for a Cloud-Control-only module; `cmd/ci/` + `internal/ci/` show the mixed pattern (Cloud Control IAM role + SDK-backed CodeBuild project).

## Conventions

**Naming:**
- Packages: lowercase single-word (`perforce`, `horde`, `state`)
- Files: `snake_case.go`
- Acronyms fully uppercase: `ID`, `ARN`, `URL`, `AWS`, `IAM`
- `New*` constructors return pointers; single-letter receivers

**Imports:** stdlib group, blank line, then everything else. `gofmt` only — no goimports or gofumpt.

**Config structs:** always add `mapstructure:` tags. Live in `internal/config/config.go`.

**Error handling:** `fmt.Errorf("context: %w", err)`. Messages state what went wrong AND what to do. No sentinel errors.

**Cost estimation:** every new resource type needs a cost estimator registered by `TypeName` via `cost.Global.Register`. Do not re-register `AWS::EC2::Instance` or `AWS::EC2::Volume` — already registered in `internal/perforce/cost.go`.

**No logging library:** `fmt.Printf`/`Println` only.

**Coverage target:** 60%+ for `internal/*`; tests use mocked SDK interfaces — no real AWS calls.

## Planned Command Structure

Per-module status and phase sequencing live in [`ROADMAP.md`](ROADMAP.md) — the command tree below is the full target surface.

```
fabrica setup                               # guided first-run provisioning wizard
fabrica status                              # health of all modules
fabrica perforce create|status|destroy      # ✓ implemented; backup|restore planned
fabrica horde create|status|submit|destroy  # ✓ implemented
fabrica horde ami build                     # ✓ implemented; generates Image Builder recipe + optional Packer HCL
fabrica ci setup|trigger|status|logs        # ✓ implemented; CodeBuild orchestration over Horde
fabrica deploy setup|promote|rollback|status|destroy  # ✓ implemented; GameLift blue/green deployment
fabrica workstation create|list|stop|start|terminate  # ✓ implemented
fabrica cost report|forecast|alerts         # ✓ implemented; offline cost visibility + local budget alerts
fabrica doctor                              # prerequisite validation
fabrica destroy --all                       # clean teardown
fabrica export --format cloudformation      # escape hatch
```

## Workstation-Specific Notes

- **AMI-first provisioning** — The AMI must already have NICE DCV installed. Fabrica only configures and starts the DCV session via cloud-init. See AWS NICE DCV documentation for AMI requirements.
- **No credentials in UserData** — DCV session password is written to `.fabrica/workstation-credentials.yaml` (mode 0600) only; never embedded in UserData.
- **Port** — 8443 (NICE DCV HTTPS). The default `allowedCidr` is `0.0.0.0/0`; the create command warns when this default is used. Restrict to a VPN CIDR in production via `workstation.allowedCidr` in `fabrica.yaml`.
- **State version** — `UpsertModule` stores `plan.AmiID` as the module version (same pattern as horde), so state tracks which AMI was used.
- **State status transitions** — `provisioning` → `ready` (set by future status command); `stop` sets `"stopped"`; `start` sets `"ready"`; `terminate` removes the module entirely.
- **Templates** — `--template artist` → `g6.xlarge` + 200 GiB; `--template programmer` → `c7i.xlarge` + 100 GiB. Template overrides config defaults; explicit `--instance-type`/`--volume-size` flags further override.
- **`--mount-perforce`** — reads the Perforce module's instance private IP from local state via `rt.Provider.Resources().Get(...)`, then injects `P4PORT=<ip>:1666` into `~/.p4config` via cloud-init. Requires the `perforce` module to be provisioned. Developer runs `p4 sync` manually.
- **Stop/start confirmation** — uses `prompt.Confirm` (simple y/N), not `prompt.ConfirmExact` (typed phrase). Terminate uses `ConfirmExact` (same as perforce/horde destroy).
- **GPU instance prices** — g4dn, g5, g6, and c7i family prices live in `internal/perforce/cost.go` alongside the shared EC2/EBS estimators. Do not add a separate cost registration for workstation resources.
- **Idle timeout** — `workstation.idleTimeoutMinutes` in `fabrica.yaml` (default 60) is injected into the DCV cloud-init; the constant `DefaultIdleTimeoutMinutes` lives in `internal/workstation/config.go`.

## Horde-Specific Notes

- **AMI-first provisioning** — Horde AMI must contain MongoDB 7, Redis 6.2, and the Horde server binary. Fabrica only configures and starts services via cloud-init. See `docs/horde-ami.md`.
- **No credentials in UserData** — `ec2:DescribeInstanceAttribute` exposes UserData to anyone with that permission. MongoDB password is written to `.fabrica/horde-credentials.yaml` (mode 0600) only.
- **Ports** — 5000 (HTTP/web UI), 5002 (gRPC for agents). Status probes port 5000 only.
- **Submit URL** — `hordeHTTPClient` uses the instance's private IP from Cloud Control. Requires VPN or same-VPC access; no public IP in V1.
- **`horde_service_token`** in credentials is optional; if empty the auth header is omitted (Horde returns 401 if a token is required).

## CI-Specific Notes

- **Orchestration layer over Horde** — `ci` does not replace Horde; CodeBuild is the conductor and Horde stays the BuildGraph executor. `ci trigger` parses the BuildGraph (reusing `internal/horde/buildgraph`), resolves Horde's private IP from state, and starts a CodeBuild build whose buildspec POSTs the job to Horde. `horde submit` remains the low-level direct-to-Horde path.
- **CodeBuild is NOT Cloud Control** — `AWS::CodeBuild::Project` returns `UnsupportedActionException` for the Cloud Control CREATE action. The project is created/deleted via the `cloud.CodeBuildRunner` SDK auxiliary interface (`internal/cloud/aws/codebuild.go`); only the IAM role goes through Cloud Control. This is the same Cloud-Control-plus-SDK split as `EC2InstanceManager`/`StateBackendBootstrapper`. When adding resource types, verify Cloud Control support before assuming `rt.Provider.Resources().Create` works.
- **`ci trigger` semantics** — V1 starts the CodeBuild project directly. The design intends `trigger` to start a CodePipeline execution once CodePipeline orchestration is added (deferred); the command surface stays stable.
- **Idempotency** — `EnsureProject` checks `BatchGetProjects` before creating, so re-running `ci setup` is safe. `DeleteProject` is idempotent on the AWS side.
- **Tags** — Cloud Control desired-state and the CodeBuild SDK both take tags as a capitalized `Tags`/`[]Tag` shape; `injectFabricaTags` (applied to every Cloud Control create) merges into the `Tags` array, never a lowercase `tags` key (which Cloud Control schemas reject).
- **Out of scope (V1)** — CodePipeline + source wiring, `ci destroy`/teardown in `destroy --all`, active Perforce sync inside builds (buildspec has a documented placeholder).

## Deploy-Specific Notes

- **GameLiftManager SDK split** — Like CI's `CodeBuildRunner`, GameLift resource operations that Cloud Control doesn't fully support (fleet activation polling, fleet events retrieval) go through the `cloud.GameLiftManager` SDK auxiliary interface (`internal/cloud/aws/gamelift.go`) while Build, Fleet, and Alias creation use Cloud Control. Fleet activation (20–40 min) cannot be exposed by Cloud Control's blocking waiters because they don't surface fleet phases or activation-failure events; `FleetStatus` + `FleetEvents` bridge this gap.
- **Non-blocking fleet create via CreateFleetAsync** — `promote` starts fleet creation with a non-blocking Cloud Control path that returns as soon as the FleetId is assigned, then immediately polls `FleetStatus` to wait for ACTIVE state. This avoids tying up the CLI while the fleet provisions.
- **Alias-flip blue/green** — `promote` always creates a new fleet and flips the alias to it only once ACTIVE. The previous fleet is retained (not deleted) so `rollback` is an instant alias flip — no re-provisioning. This is the canonical GameLift blue/green pattern.
- **Retain prior fleet for rollback** — `promote` stores the previous active fleet ID in state so `rollback` can flip the alias back instantly. `status` shows rollback candidates — fleets with a prior alias-flip history. Only the most recent prior fleet is a rollback target; older fleets remain in state but cannot be rolled back to.
- **Destroy default vs. `--all` semantics** — `destroy` (default) deletes fleets + builds but leaves the alias and IAM role in place so the alias your game backend references survives teardown and you can re-promote later. `--all` also removes the alias + role — use only if tearing down the entire deploy infrastructure. The `cmd/internal/teardown` engine handles resource deletion order via the `ResourceOrder` hook.
- **`deploy.buildBucket` required** — The S3 bucket where CI/Horde uploads packaged builds must be set in `fabrica.yaml`. `promote` uses the convention `s3://<buildBucket>/builds/<build-version>/server.zip` by default; override with `--s3-bucket`/`--s3-key`.
- **S3 build convention** — CI/Horde outputs are expected at `s3://<buildBucket>/builds/<build-version>/server.zip`. This path is hardcoded in the build registration; Fabrica does not upload builds, only registers and deploys what's already there.

## Cost-Specific Notes

- **Config-derive model** — `cost report` and `cost forecast` reflect the *current* `fabrica.yaml` estimates, scoped to modules present in local state (no S3 roundtrip). Fully offline — the `costsource.Aggregate` engine reads state + config and returns a `[]CostResource`, which the cost package projects and evaluates. No AWS calls; no cost-data API.
- **Stopped instances drop the compute line** — When `workstation stop` sets status to `"stopped"`, the `costsource` engine filters that instance out of the cost model (state still tracks it). EBS volumes remain billed. `workstation start` returns the instance to the cost model.
- **Deploy fleet cost counted only when a fleet exists** — If no `AWS::GameLift::Fleet` resource exists in the deploy module state, the deploy cost is zero. Once a fleet is promoted (created and ACTIVE), it enters the cost model until `destroy` removes it.
- **Local thresholds only** — `cost alerts set/list/check` work entirely on the local `fabrica.yaml` — no AWS Budgets resources, no SNS topics, no CloudWatch events. `alerts check` is informational (exit 0 always); the caller can decide what to do with OK/WARN/OVER status.
- **Follow-up: Properties backfill deferred** — `ModuleResource.Properties` could store cost-config metadata at create time (e.g., instance type, EBS size), allowing cost reports to read state without the config file. This is a V2 convenience feature; V1 requires both state + config to coexist.
