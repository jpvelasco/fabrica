# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What Fabrica Is

A Go CLI + infrastructure-as-code framework that provisions and manages game studio cloud infrastructure on AWS: Perforce Helix Core, Unreal Horde build farms, CI/CD, GameLift deployment, and cloud workstations. Sister tool to [Ludus](https://github.com/jpvelasco/ludus) — while Ludus handles a single developer's pipeline, Fabrica scales that to a full studio.

## Project Status

Phase 0 (CLI skeleton + AWS foundation) is complete. The state backend, `doctor`, `setup`, `destroy`, and `configcmd` commands are implemented. The `perforce` module (create/status/destroy) is the first fully-wired module — Cloud Control calls are live but the CloudControl stub (`internal/cloud/aws/cloudcontrol.go`) is still used for the broader resource API.

The `src/` directory contains a parallel C# CDK exploration (`Fabrica.CdkApp`, `Fabrica.Cli`, `Fabrica.Constructs`, `Fabrica.Operations`) — this is not the active implementation path; Go is the chosen stack.

## Build Commands

```bash
go build ./...
go vet ./...
go test ./...                          # Windows (no -race)
go test -race -coverprofile=coverage.out -covermode=atomic ./...  # Linux/macOS
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

### Module Pattern

`internal/perforce` is the canonical template for new modules:
- **Pure plan layer** — no AWS SDK imports. Builds `CreatePlan` and Cloud Control desired-state JSON. The `cmd/<module>` layer calls the plan layer, then executes via `rt.Provider.Resources()`.
- **VPCResolver interface** — when a module needs AWS-specific resolution (VPC, subnet), define an interface in `internal/<module>` that the provider implements. Keeps `internal/*` SDK-free.
- **Cost estimators via `init()`** — register in `cost.Global` from `internal/<module>/cost.go`; imported transitively by the create command.
- **`GenerateRaw`** — when a function produces base64 output, add a `*Raw` variant returning the plain string for test assertions.
- **State written after each resource** — partial failures leave a recoverable record; re-running detects already-provisioned state and exits cleanly.

### Provider Registration

`internal/cloud/aws/aws.go` registers the AWS provider via a blank-import side-effect (`_ "github.com/jpvelasco/fabrica/internal/cloud/aws"` in `cmd/root`). New providers follow the same `init()` pattern against `internal/cloud/registry.go`.

### Config + State

Config: `fabrica.yaml` (or `fabrica-<profile>.yaml` with `--profile`). State: S3 bucket (`fabrica-state-<account-id>`) + DynamoDB table (`fabrica-state-lock`) remote, with `.fabrica/state.json` local cache.

## Architecture Decisions (Locked)

- **IaC:** AWS Cloud Control API (`aws-sdk-go-v2/service/cloudcontrol`) — no Terraform, no Pulumi, no external binaries
- **Module path:** `github.com/jpvelasco/fabrica`
- **Config:** Viper + YAML — Viper scoped inside `internal/config` only; no logging library, `fmt.Printf`/`Println` only

## Test Strategy

The destroy command uses a two-package test approach — a pattern to follow for other commands as they are implemented:

- `destroy_test.go` (`package destroy`) — white-box tests that call `command.run()` directly. Used for confirmation rejection paths, partial failures, and any behavior that requires access to unexported fields (e.g. the `confirm` injection seam).
- `cobra_test.go` (`package destroy_test`) — black-box Cobra-layer tests that call `destroy.New(...) + ExecuteContext`. These exercise flag parsing (`--all`, `--dry-run`, `--yes`), command construction, and output. They build a minimal root command in the test to replicate the persistent-flag hierarchy (`--dry-run`, `--yes` live on root, not on destroy).

## Conventions

See `AGENTS.md` for the full coding conventions. Key additions over Ludus:
- `mapstructure:` tags required on all config structs
- `gofmt` only (no goimports/gofumpt)
- Coverage target: 60%+ for `internal/*`; tests use mocked SDK interfaces — no real AWS calls

## Planned Command Structure

```
fabrica setup                          # guided first-run provisioning wizard
fabrica status                         # health of all modules
fabrica perforce create|status|destroy      # ✓ implemented; backup|restore planned
fabrica horde [setup|status|scale|workers]
fabrica ci [setup|trigger|status|logs]
fabrica deploy [setup|promote|status|destroy]
fabrica workstation [create|list|stop|terminate]
fabrica cost [report|forecast|alerts]
fabrica doctor                         # prerequisite validation
fabrica destroy --all                  # clean teardown
fabrica export --format cloudformation # escape hatch
```

## V1 Scope

In scope: CLI skeleton, `fabrica setup` wizard, Perforce Helix Core (single-server + S3 backup), Horde build farm (coordinator + auto-scaling workers), BuildGraph XML ingestion from Ludus, cost estimation before provisioning, `fabrica status`, `fabrica doctor`, `fabrica destroy`.

Out of scope for V1: workstations (NICE DCV), multi-region, CI/CD pipeline, web dashboard, multi-cloud, MCP server.

## Open Questions

- Horde vs. simpler custom job distribution for V1
- Perforce licensing strategy for teams > 5 users
- `fabrica.yaml` / `ludus.yaml` config sharing approach
- Open source vs. commercial vs. open-core pricing model
