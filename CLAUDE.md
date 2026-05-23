# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What Fabrica Is

A Go CLI + infrastructure-as-code framework that provisions and manages game studio cloud infrastructure on AWS: Perforce Helix Core, Unreal Horde build farms, CI/CD, GameLift deployment, and cloud workstations. Sister tool to [Ludus](https://github.com/jpvelasco/ludus) — while Ludus handles a single developer's pipeline, Fabrica scales that to a full studio.

## Project Status

Phase 0 (CLI skeleton + AWS foundation) is complete. The `perforce` module (create/status/destroy) and `horde` module (create/status/submit) are fully implemented. Cloud Control calls are live; the CloudControl stub (`internal/cloud/aws/cloudcontrol.go`) is still used for the broader resource API in non-perforce/horde paths.

The `src/` directory contains a parallel C# CDK exploration — this is not the active implementation path; Go is the chosen stack.

## Build Commands

```bash
go build ./...
go vet ./...
go test ./...                          # Windows (no -race)
go test -race -v ./...                 # macOS
go test -race -coverprofile=coverage.out -covermode=atomic ./...  # Linux only
go test ./... -run TestName            # single test
golangci-lint run ./...
go tool cover -func=coverage.out       # coverage summary
```

CI runs lint + build + test cross-platform (ubuntu/windows/macos) on push/PR to main.

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
| `cmd/{destroy,doctor,setup,configcmd,version}` | Subcommands; each `New()` accepts `RuntimeSource` + `OptionsSource` closures — no direct globals access |
| `internal/config` | `Config` struct, Viper loading from `fabrica.yaml` (scoped here only), YAML serialization, defaults |
| `internal/cloud` | Provider-agnostic interfaces: `Provider`, `ResourceClient`, `Resource`, `StateBackendDestroyer` |
| `internal/cloud/aws` | AWS implementation registered via `init()` in `internal/cloud/registry.go`; wraps `cloudcontrol`, `s3`, `dynamodb`, `iam` SDK clients |
| `internal/state` | `State`/`ModuleState`/`ModuleResource` types, `Backend` interface, S3+DynamoDB bootstrap, DynamoDB locking |
| `internal/cost` | Cost estimator interface + Phase 0 estimators; registered by resource `TypeName` |
| `internal/tags` | Tag injection helpers; `ManagedBy: fabrica` applied to all resources |
| `internal/prompt` | `ConfirmExact` for interactive confirmation dialogs |
| `internal/version` | Version constant |
| `cmd/perforce` | Parent command; wires create/status/destroy subcommands |
| `cmd/perforce/create` | Provision SG + EC2 instance in order; writes state after each; generates credentials |
| `cmd/perforce/status` | Reads state + Cloud Control live data; TCP-probes port 1666; transitions provisioning→ready |
| `cmd/perforce/destroy` | Deletes EC2 instance then SG in reverse order; skips already-terminated instances |
| `internal/perforce` | Pure plan layer (no SDK): version resolution, `CreatePlan`, Cloud Control JSON builders (`SGDesiredState`, `InstanceDesiredState`), cloud-init generator, cost estimators |
| `cmd/horde` | Parent command; wires create/status/submit subcommands |
| `cmd/horde/create` | Provision SG + EC2 instance (AMI-first); generates MongoDB password; writes credentials to `.fabrica/horde-credentials.yaml` |
| `cmd/horde/status` | Reads state + Cloud Control live data; TCP-probes port 5000 (HTTP); transitions provisioning→ready; `--json` emits `hordeUrl`/`hordeGrpc` |
| `cmd/horde/submit` | Parses BuildGraph XML; resolves coordinator private IP via Cloud Control; POSTs to Horde REST API; supports `--wait` polling |
| `internal/horde` | Pure plan layer: `CreatePlan`, `SGDesiredState`, `InstanceDesiredState`, cloud-init generator (`Generate`/`GenerateRaw`), `VPCResolver` interface |
| `internal/horde/buildgraph` | Isolated sub-package: `ParseBuildGraph(path)` → `*BuildGraphJob`; XML-only, no AWS/HTTP deps |

### Module Pattern

`internal/perforce` and `internal/horde` are the canonical templates for new modules:
- **Pure plan layer** — no AWS SDK imports. Builds `CreatePlan` and Cloud Control desired-state JSON. The `cmd/<module>` layer calls the plan layer, then executes via `rt.Provider.Resources()`.
- **VPCResolver interface** — when a module needs AWS-specific resolution (VPC, subnet), define an interface in `internal/<module>/config.go` that the provider implements. Keeps `internal/*` SDK-free.
- **Cost estimators** — m5/c5 (Perforce) and m7i (Horde) EC2 prices live together in `internal/perforce/cost.go`. `cost.Global.Register` panics on duplicate `TypeName` — do not register `AWS::EC2::Instance` or `AWS::EC2::Volume` from a second package.
- **`GenerateRaw`** — when a function produces base64 output, add a `*Raw` variant returning the plain string for test assertions.
- **State written after each resource** — partial failures leave a recoverable record; re-running detects already-provisioned state and exits cleanly.
- **Config structs in `internal/config/config.go`** — `HordeConfig` and `PerforceConfig` both live here (not in their respective `internal/<module>` packages) to avoid circular imports.

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

Seam pattern: the `command` struct holds `func` fields for all I/O operations. `New()` wires real implementations; tests inject fakes. `fakeProvider` / `fakeHordeClient` patterns live in `*_test.go` files alongside the tests that use them.

## Conventions

See `AGENTS.md` for the full coding conventions. Key additions over Ludus:
- `mapstructure:` tags required on all config structs
- `gofmt` only (no goimports/gofumpt)
- Coverage target: 60%+ for `internal/*`; tests use mocked SDK interfaces — no real AWS calls

## Planned Command Structure

```
fabrica setup                               # guided first-run provisioning wizard
fabrica status                              # health of all modules
fabrica perforce create|status|destroy      # ✓ implemented; backup|restore planned
fabrica horde create|status|submit          # ✓ implemented
fabrica horde destroy                       # planned; follows perforce/destroy pattern
fabrica ci [setup|trigger|status|logs]
fabrica deploy [setup|promote|status|destroy]
fabrica workstation [create|list|stop|terminate]
fabrica cost [report|forecast|alerts]
fabrica doctor                              # prerequisite validation
fabrica destroy --all                       # clean teardown
fabrica export --format cloudformation      # escape hatch
```

## Horde-Specific Notes

- **AMI-first provisioning** — Horde AMI must contain MongoDB 7, Redis 6.2, and the Horde server binary. Fabrica only configures and starts services via cloud-init. See `docs/horde-ami.md`.
- **No credentials in UserData** — `ec2:DescribeInstanceAttribute` exposes UserData to anyone with that permission. MongoDB password is written to `.fabrica/horde-credentials.yaml` (mode 0600) only.
- **Ports** — 5000 (HTTP/web UI), 5002 (gRPC for agents). Status probes port 5000 only.
- **Submit URL** — `hordeHTTPClient` uses the instance's private IP from Cloud Control. Requires VPN or same-VPC access; no public IP in V1.
- **`horde_service_token`** in credentials is optional; if empty the auth header is omitted (Horde returns 401 if a token is required).
