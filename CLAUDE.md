# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What Fabrica Is

A Go CLI + infrastructure-as-code framework that provisions and manages game studio cloud infrastructure on AWS: Perforce Helix Core, Unreal Horde build farms, CI/CD, GameLift deployment, and cloud workstations. Sister tool to [Ludus](https://github.com/jpvelasco/ludus) — while Ludus handles a single developer's pipeline, Fabrica scales that to a full studio.

## Project Status

[`ROADMAP.md`](ROADMAP.md) is the single source of truth for phases, module status, and the Praetorium vision. Update it when module status changes.

Phase 0 (CLI skeleton + AWS foundation) is complete. Three modules are fully implemented: `perforce` (create/status/destroy), `horde` (create/status/submit/destroy/ami), and `workstation` (create/list/stop/start/terminate). All five `ResourceClient` methods in `internal/cloud/aws/cloudcontrol.go` are implemented against the real Cloud Control API — new modules can use `rt.Provider.Resources()` without routing through module-specific SDK wrappers.

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
| `internal/cloud` | Provider-agnostic interfaces: `Provider`, `ResourceClient`, `Resource`, `EC2InstanceManager`, `StateBackendChecker` (doctor/status: bucket/table exists checks), `StateBackendBootstrapper` (setup: create bucket/table), `StateBackendDestroyer` (destroy --all: delete bucket/table) |
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

### Module Pattern

`internal/perforce` and `internal/horde` are the canonical templates for new modules:
- **Pure plan layer** — no AWS SDK imports. Builds `CreatePlan` and Cloud Control desired-state JSON. The `cmd/<module>` layer calls the plan layer, then executes via `rt.Provider.Resources()`.
- **VPCResolver interface** — when a module needs AWS-specific resolution (VPC, subnet), define an interface in `internal/<module>/config.go` that the provider implements. Keeps `internal/*` SDK-free.
- **EC2InstanceManager** — Cloud Control cannot stop or start EC2 instances. Use the `cloud.EC2InstanceManager` auxiliary interface (defined in `internal/cloud/ec2manager.go`, implemented by `awsProvider` in `internal/cloud/aws/`). Access via type assertion: `rt.Provider.(cloud.EC2InstanceManager)`. Follow the `state_backend.go` pattern when adding future provider-specific capabilities.
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
2. **`cmd/<module>/`** — Cobra command with `RuntimeSource` + `OptionsSource` closures; seam fields on the `command` struct for all I/O.
3. **Config struct** — add to `internal/config/config.go` (not `internal/<module>/`) with `mapstructure:` tags to avoid circular imports.
4. **Cost estimators** — register via `cost.Global.Register` in the plan layer. Do not re-register `AWS::EC2::Instance` or `AWS::EC2::Volume` — already registered in `internal/perforce/cost.go`.
5. **Wire** the parent command in `cmd/root/root.go`.
6. **Tests** — two-file pattern: white-box `*_test.go` + black-box `cobra_test.go` with a minimal root that replicates the persistent-flag hierarchy.

Reference: `cmd/perforce/` + `internal/perforce/` are the canonical templates.

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
fabrica ci [setup|trigger|status|logs]
fabrica deploy [setup|promote|status|destroy]
fabrica workstation create|list|stop|start|terminate  # ✓ implemented
fabrica cost [report|forecast|alerts]
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
