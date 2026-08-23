# Fabrica — Agent Instructions

## Project Overview

Go CLI that provisions game studio cloud infrastructure on AWS. Single binary, zero external dependencies. Sister tool to [Ludus](https://github.com/jpvelasco/ludus) — Ludus orchestrates game builds, Fabrica gives them somewhere to run.

**Current state:** All phases implemented — Phase 0, Phase 1 (core pipeline), Lore (v0.2), and DDC (V1 + multi-region edge nodes) are complete; current stable release **v0.4.2** (2026-08-19). Modules implemented: `perforce`, `horde`, `lore`, `ddc`, `workstation`, `ci`, `deploy`, `cost`, plus read-only `status`, `doctor`, `drift`, `config show`, `export`, `mcp`, full-stack `destroy --all`, and a CLI E2E test suite. See [ROADMAP.md](ROADMAP.md) for the authoritative, current module status.

**Private designs:** Draft specs and implementation plans for future work live under `.private/` (gitignored — never commit). Suggested layout: `.private/designs/`, `.private/plans/`. Public user docs stay in `docs/` (AMI guides, deploy notes) and root `README.md` / `ROADMAP.md`.

## Current Modules

| Module | Commands | What it does |
|--------|----------|--------------|
| foundation | `setup`, `status`, `doctor`, `drift`, `config show`, `version` | S3 + DynamoDB state backend, aggregate health overview, env checks, read-only drift detection against live AWS, config display, version |
| `perforce` | `create`, `status`, `destroy`, `backup`, `restore` | Provisions a Perforce Helix Core EC2 instance with SG + SSM instance profile; tracks provisioning state; TCP probe on 1666; EBS backup/restore via SSM |
| `horde` | `create`, `status`, `submit`, `destroy`, `ami build` | Provisions an Unreal Horde build coordinator (AMI-first, m7i.2xlarge) with SG + IAM SSM instance profile; probes port 5000; parses BuildGraph XML and POSTs jobs to the Horde REST API; generates EC2 Image Builder recipe + optional Packer HCL for building the required AMI |
| `lore` | `create`, `status`, `destroy` | Provisions an Epic Lore (`loreserver`) EC2 instance (AMI-first, local/EBS store); probes `GET /health_check` on port 41339; parallel to Perforce |
| `ddc` | `setup`, `status`, `destroy`, `region add` | Provisions Unreal Cloud DDC (Jupiter) on EC2 (AMI-first, home region + additional edge regions); hybrid EBS+S3; default `zen` backend; probes `GET /health/ready` |
| `workstation` | `create`, `list`, `stop`, `start`, `terminate` | Provisions a NICE DCV cloud workstation on EC2 (AMI-first, g4dn.xlarge default); allows TCP 8443 inbound; writes DCV session credentials to `.fabrica/workstation-credentials.yaml`; supports stop/start via EC2InstanceManager and permanent termination |
| `ci` | `setup`, `trigger`, `status`, `logs`, `destroy` | CodeBuild orchestration over Horde; IAM role via Cloud Control, CodeBuild project via SDK auxiliary interface |
| `deploy` | `setup`, `promote`, `rollback`, `status`, `destroy` | GameLift blue/green deployment; fleet activation polling via SDK auxiliary interface |
| `cost` | `report`, `forecast`, `alerts` | Offline config-derived reporting + local budget alerts |
| `export` | `--format cloudformation\|terraform` | Generates IaC templates from recorded local state only — no live AWS calls; V1 covers state backend + horde/perforce/lore; secrets redacted |
| `mcp` | `mcp` | stdio MCP server exposing 6 read-only tools; reuses the same `internal/*` logic as the CLI |

## Current Known Limitations

- **State backend is created by `fabrica setup`.** Provisions the S3 state bucket (versioning + encryption + public-access-block) and the DynamoDB lock table used for distributed state locking (15-minute TTL with stale takeover), idempotently — shows a plan + cost estimate and prompts before any write (`--yes` skips, `--dry-run` previews). Run it once before other commands.
- **Horde requires a user-provided AMI.** AMI must already contain MongoDB 7, Redis 6.2, and the Horde server binary. See [docs/horde-ami.md](docs/horde-ami.md).
- **DDC edges reuse the home stack.** Edge AMIs are region-specific; copy the home AMI first (`aws ec2 copy-image`). Cross-region replication between edges is operator-managed. See [docs/ddc-ami.md](docs/ddc-ami.md).

## Architecture Overview

### Dependency Flow

```
cmd/* → internal/{config, state, cost, tags, prompt, cloud}
                                                    ↓
                                        internal/cloud/aws
```

`internal/cloud/*` never imports `internal/state`, `internal/cost`, or any `cmd/*`. Verify after changes:

```bash
go list -deps ./internal/cloud/...
```

### Key Patterns

**SDK-free `internal/*`** — `internal/perforce`, `internal/horde`, `internal/lore`, `internal/ddc`, and `internal/workstation` are pure plan layers with no AWS SDK imports. They build `CreatePlan` structs and Cloud Control desired-state JSON. The `cmd/<module>` layer calls the plan layer, then executes via `rt.Provider.Resources()`.

**EC2InstanceManager for stop/start** — Cloud Control API only does CRUD and cannot stop or start EC2 instances. The `cloud.EC2InstanceManager` interface (defined in `internal/cloud/ec2manager.go`) exposes `StopInstance` / `StartInstance`. The AWS provider implements it in `internal/cloud/aws/ec2manager.go` via the EC2 SDK. Commands access it via type assertion: `rt.Provider.(cloud.EC2InstanceManager)`. Follow the `state_backend.go` auxiliary-interface pattern for any future provider-specific capabilities.

**Verify Cloud Control support per resource type** — not every CloudFormation type supports the Cloud Control CREATE action. `AWS::CodeBuild::Project` throws `UnsupportedActionException`, so the `ci` module creates it through the `cloud.CodeBuildRunner` SDK auxiliary interface (IAM role still via Cloud Control); `deploy` adds `cloud.GameLiftManager` for fleet status/events. Confirm support before assuming `rt.Provider.Resources().Create` works; if it doesn't, add an SDK-backed auxiliary interface.

**Build new EC2 modules on the shared packages** — `internal/ec2plan` (plan base), `internal/ec2state` (desired-state JSON), `internal/ec2cost` (cost resources), `internal/userdata` (cloud-init rendering), and `internal/iamrole` (trust policies) exist so a new single-instance module doesn't reintroduce the duplication they centralized. `internal/lore` and `internal/ddc` are the reference consumers — `internal/perforce` predates these packages and duplicates what they now own. Shared command orchestration lives in `cmd/internal/`: `provision` (create lifecycle), `modstatus` (status spine + per-command `Renderer`), `teardown` (destroy engine), `destroyall`, `costsource`, `doctorchecks` + `statusreport` (doctor/status logic extracted so the MCP server reuses it), and `testutil` (Cobra/cloud test fakes).

**Seam injection for testability** — the `command` struct holds `func` fields for all I/O operations (`readState`, `writeState`, `createResource`, `probeTCP`, etc.). `New()` wires real implementations; tests inject fakes. No global state, no `init()` side effects in tests.

**Two-package test pattern:**
- `*_test.go` (`package <cmd>`) — white-box tests calling `command.run()` directly with injected seams
- `cobra_test.go` (`package <cmd>_test`) — black-box Cobra-layer tests calling `cmd.New(...).ExecuteContext()`

**Incremental state** — state is written after each resource creation. Partial failures leave a recoverable record; re-running detects already-provisioned resources and exits cleanly.

**VPCResolver interface** — when a module needs AWS-specific resolution, define an interface in `internal/<module>/config.go` that the provider implements. Keeps `internal/*` SDK-free.

**VPC wiring via the shared helper** — create/setup commands pass `provision.VPCResolver(rt.Provider)` into their plan constructors; never re-type-assert inline and never pass `nil`. With config `vpcId`/`subnetId` absent, plans resolve the account default VPC; a half-specified pair (one set, one empty) fails fast via `topology.ResolveVPC`.

**Context-aware polling** — every polling interval goes through `provision.WaitInterval(ctx, d)` via a `waitCtx`/`SleepCtx` seam, so Ctrl+C ends waits immediately instead of after the full interval. Never wire bare `time.Sleep`; never call `http.Get` without `NewRequestWithContext`.

**Distributed state locking** — every state-mutating flow acquires the account-level DynamoDB lock via `provision.AcquireStateLock(ctx, rt, operation)` at entry and defers the release. Nested orchestration (`destroy --all` → module teardowns) inherits the lock through a ctx sentinel and no-ops. The lock carries a 15-minute TTL with stale takeover, so a crashed holder cannot deadlock anyone. Providers without `cloud.StateLockManager` (fakes/E2E) get a silent no-op. `setup` (and any command on an unbootstrapped account) proceeds unlocked when the table is absent — the adapter maps ResourceNotFoundException to `cloud.ErrLockTableMissing`; don't "fix" this into a hard failure, it's the bootstrap ordering.

**Provider capability asserts target `*awsProvider`** — auxiliary interfaces implemented on the embedded `ec2Service`/dynamo clients need an explicit delegating method on `awsProvider` (e.g. `TagInstanceVolumes`, `AcquireStateLockRow`), or `rt.Provider.(cloud.X)` fails silently and flows no-op. Add a `var _ cloud.X = (*awsProvider)(nil)` guard next to every delegate; unit-test through the provider, not the service.

**SDK-first deletion hooks** — resources Cloud Control can't delete directly use the teardown engine's `SDKDeleteFunc(ctx, typeName, identifier)` seam: return `cloud.ErrNotHandled` to fall through to Cloud Control. Modules attach it through `teardown.Spec.WireCommand` so both standalone and orchestrated paths get it (CI: project deletion; lore: purge the versioned store bucket first via `cloud.S3BucketCleaner`).

**Concurrency-safe lazy init** — the MCP server dispatches tool calls concurrently against one provider. All lazy client construction in `internal/cloud/aws` (`Resources()`, `resourceClients.ensureClient`, `ec2Service.ensureClient`) is mutex-guarded; keep new lazy-init sites synchronized.

**Embedded templates** — `cmd/horde/ami` ships build artifacts as `embed.FS` templates rendered with `text/template`. New file-generator commands should follow this pattern: templates under `cmd/<cmd>/templates/`, rendered via a `renderTemplate` helper on the command struct. No `RuntimeSource`/`OptionsSource` needed when the command makes no AWS calls.

**Provider registration** — `internal/cloud/aws/aws.go` registers the AWS provider via a blank-import side-effect (`_ "github.com/jpvelasco/fabrica/internal/cloud/aws"` in `cmd/root`). New providers follow the same `init()` pattern against `internal/cloud/registry.go`.

**Config + State** — `fabrica.yaml` (or `fabrica-<profile>.yaml` with `--profile`). Copy `fabrica.example.yaml` for a starting point. State: S3 bucket (`fabrica-state-<account-id>`) + DynamoDB table (`fabrica-state-lock`) remote, with `.fabrica/state.json` local cache.

**Output** — dual streams: human output via `fmt.Printf`/`Println` to stdout; operational diagnostics via `internal/oplog` (stdlib `log/slog`) to stderr. Enable with `--verbose` or `FABRICA_LOG_LEVEL=debug`.

### Package Responsibilities

| Package | Purpose |
|---------|---------|
| `cmd/root` | Wires global flags (`--config`, `--verbose`, `--json`, `--dry-run`, `--yes`, `--profile`), initializes `globals.Store`, registers subcommands |
| `cmd/globals` | `Runtime` (Config + Provider + ConfigPath), `Options`, `Store.Init()`, dependency injection types |
| `internal/config` | `Config` struct, Viper loading from `fabrica.yaml` (scoped here only), YAML serialization, defaults |
| `internal/cloud` | Provider-agnostic interfaces: `Provider`, `ResourceClient`, `Resource`, `EC2InstanceManager`, `RemoteRunner`, `StateBackendChecker`, `StateBackendBootstrapper`, `StateBackendDestroyer`, `CodeBuildRunner`, `GameLiftManager`, `S3BucketCleaner`, `StateLockManager`, `VolumeTagger` |
| `internal/cloud/aws` | AWS implementation registered via `init()` in `internal/cloud/registry.go`; wraps `cloudcontrol`, `s3`, `dynamodb`, `iam`, `ec2` SDK clients |
| `internal/state` | `State`/`ModuleState`/`ModuleResource` types, `Backend` interface, S3+DynamoDB bootstrap; `LockStore` — TTL + stale-takeover locking wired into all state-mutating flows via `provision.AcquireStateLock` |
| `internal/cost` | Cost estimator interface + estimators; registered by resource `TypeName`. `Project`/`Forecast` for time-horizon projection; `EvaluateBudgets` for threshold evaluation. Stays free of `internal/config` — the config↔cost mapping lives in `costsource` |
| `internal/tags` | Tag injection helpers; `ManagedBy: fabrica` applied to all resources |
| `internal/prompt` | `Confirm` (y/N) and `ConfirmExact` (typed phrase) for interactive confirmation dialogs |
| `internal/version` | Version constant |
| `internal/oplog` | Stdlib-only operational logging (`log/slog`): `Init` from level/verbose, `Logger()`, `WithModule`, `WithResource`, `Redact`. Logs to stderr; safe without Init |
| `internal/credentials` | Shared helpers: `GeneratePassword`, `WriteCredentials` — write per-module credential YAML files to `.fabrica/` (mode 0600) |
| `internal/stateutil` | Shared helpers: `ResourceByType` — query module state without repeating the lookup loop |
| `internal/ec2plan` | `Base` struct + `New` constructor for fields common to every single-instance EC2 module. Embed in a module's `CreatePlan` |
| `internal/ec2state` | Shared Cloud Control desired-state builders: `InstanceDesiredState`, `SGDesiredState`/`SGIngressRule`, `InstanceProfileDesiredState` |
| `internal/ec2cost` | Shared cost-resource builders: `InstanceAndVolume`/`ResourcesWithDefaults` |
| `internal/userdata` | Shared cloud-init template helpers: `Renderer` (`Render`/`RenderBase64`); `Prepare` centralizes apply-defaults → validate chain |
| `internal/iamrole` | Shared IAM role desired-state helpers: `AssumeRolePolicyDocument(service)` |
| `internal/topology` | Provider-agnostic coordinator/edge graph types for distributed modules |
| `internal/assert` | Shared test helper: `Contains` |
| `cmd/internal/testutil` | Shared Cobra/cloud test fakes (importable only within `cmd/`): `TestProvider` and variants, `CreateTestSpec` |
| `cmd/internal/teardown` | Full engine for perforce/horde/workstation teardown commands |
| `cmd/internal/destroyall` | Orchestration engine for `destroy --all`: ordered per-module teardown then state backend |
| `cmd/internal/modstatus` | Orchestration engine for status commands; each command implements a `Renderer` |
| `cmd/internal/provision` | Shared create lifecycle and resource-step orchestration; `VPCResolver(provider)` wiring helper and `WaitInterval(ctx, d)` context-aware polling seam |
| `cmd/internal/costsource` | Shared `Aggregate` engine; sole owner of module enumeration for cost; `MapBudgets` bridges config↔cost |

### Shared command helpers rule of thumb

**Teardown shares the full engine, status shares the spine but splits rendering, and create shares the lifecycle while keeping plan construction, rendering, and resource-specific apply local.** Match the abstraction to how much is genuinely common.

## How to Add a New Command / Module

1. **Create `internal/<module>/`** — pure plan layer: `CreatePlan` struct, Cloud Control desired-state JSON builders, cloud-init generator, cost estimators. No AWS SDK imports. For a single-instance EC2 module, compose `internal/ec2plan.Base` and call `internal/ec2state` / `internal/ec2cost` / `internal/userdata` (read `internal/lore` + `internal/ddc` first — `perforce` predates these packages).
2. **Confirm Cloud Control support** for each resource type. If a type has no CREATE action, add an SDK-backed auxiliary interface in `internal/cloud` + `internal/cloud/aws` and reach it via type assertion — don't force it through `Resources()`.
3. **Create `cmd/<module>/`** — Cobra command wired with `RuntimeSource` + `OptionsSource` closures (see `cmd/perforce/` or `cmd/horde/` as templates).
4. **Add config struct** to `internal/config/config.go` (not inside `internal/<module>/`) to avoid circular imports. Add `mapstructure:` tags.
5. **Register cost estimators** in the plan layer via `cost.Global.Register`. Do NOT register `AWS::EC2::Instance` or `AWS::EC2::Volume` from a second package — they're already registered.
6. **Wire the parent command** in `cmd/root/root.go`.
7. **Tests:** follow the two-file pattern. Cover partial failures, seam errors, confirmation rejection, `--dry-run`, `--json`. Reuse `cmd/internal/testutil` cobra/cloud fakes, and document every new leaf command in `README.md` or the doc-drift guard fails (`cmd/root/docs_drift_test.go`).

Reference: `cmd/perforce/` + `internal/perforce/` are the canonical Cloud-Control-only templates; `cmd/ci/` + `internal/ci/` show the mixed Cloud-Control-plus-SDK pattern.

## Important Conventions

**Naming:**
- Packages: lowercase single-word (`perforce`, `horde`, `state`)
- Files: `snake_case.go`
- Acronyms fully uppercase: `ID`, `ARN`, `URL`, `AWS`, `IAM`
- `New*` constructors return pointers; single-letter receivers

**Imports:** stdlib group, blank line, then everything else. `gofmt` only — no goimports or gofumpt.

**Config structs:** always add `mapstructure:` tags. Live in `internal/config/config.go`.

**Error handling:** `fmt.Errorf("context: %w", err)`. Messages state what went wrong AND what to do. No ad-hoc sentinels in `cmd/*` or module layers; the narrow exception is `internal/cloud`, which defines package-level sentinels (`ErrResourceNotFound`, `ErrStateBucketNotEmpty`) that callers branch on via `errors.Is`.

**State:** always written after each resource so partial runs are recoverable.

**No logging library:** `fmt.Printf`/`Println` only.

**Cost estimation:** every new resource type needs a cost estimator registered by `TypeName`. Do not re-register `AWS::EC2::Instance` or `AWS::EC2::Volume` — already registered in `internal/perforce/cost.go`.

**Tests:**
- No real AWS calls — mock SDK interfaces
- Coverage: new/changed code must meet the Codecov `patch` gate (≥90%, enforced in CI via `codecov.yml`); no new function ships at 0%
- Use `GenerateRaw` variants for testing base64-encoded outputs (e.g., cloud-init)
- `cobra_test.go` must build a minimal root command to replicate the persistent-flag hierarchy (`--dry-run`, `--yes`, `--json` live on root)
- E2E: `test/e2e/` drives the real command tree against an in-memory fake provider (registered as `"fake"`); it runs in the default `go test ./...` — no build tag, serial only
- **Seam coverage rule (SDD):** Every new exported function must be executed by a test. If a task introduces a *seam* (a `func` field the cmd layer wires to a real impl and tests replace with a fake), a test must still exercise the real, non-seam path somewhere — a stubbed seam hides its own wiring from coverage.

## Build Commands

```bash
go build ./...                         # requires Go 1.25.13+; defaults to Version=dev Commit=unknown
go build -ldflags "-X github.com/jpvelasco/fabrica/internal/version.Version=v1.0.0 -X github.com/jpvelasco/fabrica/internal/version.Commit=$(git rev-parse --short HEAD)" .  # release build
go vet ./...
go test ./...                          # Windows (no -race)
go test -race -coverprofile=coverage.out -covermode=atomic ./...  # Linux only
go test ./... -run TestName            # single test
golangci-lint run ./...
go tool cover -func=coverage.out       # coverage summary
gofmt -w .                             # format all Go files
go list -deps ./internal/cloud/...     # layering check
```

`make ci` (lint + vet + build + test) is the full local gate, but its `test`/`cover` targets use `-race` — run `go test ./...` directly on Windows instead.

### Linting

`.golangci.yml` (v2 schema) starts from `default: none` and explicitly enables: `errcheck`, `govet`, `ineffassign`, `staticcheck`, `unused`, `gocritic`, `misspell`, `unconvert`, `gosec`, `dupl`. `gofmt` is the only formatter. `gosec` excludes G104 (best-effort cleanup), G204/G702/G703 (subprocess/taint-analysis noise for a CLI tool), G301/G306 (standard dir/file perms), G304 (config-file reads via variable path) — match these rationales before adding new suppressions. Codacy mirrors this via `.codacy.yml` (govet + staticcheck engines).

### CI

`.github/workflows/ci.yml` runs lint + build + test cross-platform (ubuntu/windows/macos) plus a `goreleaser build --snapshot` validation job on every push/PR to `main`. On PRs macOS is skipped.

**CI troubleshooting:** If a job fails instantly with blank logs and no steps, the job was never scheduled — check GitHub Actions billing/minutes for your account. Verify the code locally first (`go test ./... && golangci-lint run ./...`) before pushing.

### Releasing

GoReleaser builds cross-platform binaries + a GitHub Release; the `npm/` shim downloads the matching binary at install time. The pipeline is **dormant** — `.github/workflows/release.yml` fires only on a `v*` tag push. CI validates the config on every PR via a `goreleaser build --snapshot` job (build-only, never publishes).

**Cutting a release:**
1. Decide/confirm the npm package name in `npm/package.json`.
2. Set up the npm org + trusted publisher (OIDC) — one-time, see the npm-init flow.
3. Move `CHANGELOG.md` `[Unreleased]` → `[X.Y.Z]` with the date.
4. `git tag vX.Y.Z && git push origin vX.Y.Z` — this triggers `release.yml`. Nothing publishes without a tag.

## Git Hooks

Hooks live in `.githooks/` (tracked) and are inactive until enabled once per clone:

```bash
git config core.hooksPath .githooks
```

- **pre-commit** — `gofmt -l` + `go vet` on staged Go files
- **commit-msg** — enforces Conventional Commits (`feat|fix|refactor|test|docs|chore|perf|ci|build`)
- **pre-push** — fails if any changed (non-test) function is at 0.0% coverage (early warning; the CI Codecov `patch` ≥90% gate is the real authority)

## Test Strategy

**Two-file pattern:** `*_test.go` (white-box, `package <cmd>`) + `cobra_test.go` (black-box, `package <cmd>_test`). Seam fields on the `command` struct for all I/O. `New()` wires real implementations; tests inject fakes.

**E2E test harness:** `test/e2e/` is a black-box (`package e2e`) CLI end-to-end suite that drives the real `cmd/root` command tree against an in-memory fake `cloud.Provider` (registered as `"fake"`). No real AWS, no build tag — it runs in the default `go test ./...` and CI. The fake's store lives in a per-test holder (`currentFake`), reset by `setupE2E(t)`; tests are serial (no `t.Parallel()`). To add a flow: write a new `test/e2e/<flow>_test.go`, call `setupE2E(t)`, drive commands with `runCLI`, and assert the triad — exit code + output assertions (`assertContains`/`assertJSON`) + state assertions (read `.fabrica/state.json` and call `assertModule*` checkers). Real-AWS coverage remains the separate manual `//go:build integration` suite.

## Key Decisions (Locked)

- **Module path:** `github.com/jpvelasco/fabrica`
- **IaC:** AWS Cloud Control API (`aws-sdk-go-v2/service/cloudcontrol`) — no Terraform, no Pulumi, no external binaries
- **State backend:** S3 + DynamoDB (DynamoDB-locked, 15-min TTL with stale takeover) + `.fabrica/state.json` (local cache)
- **Config:** Viper + YAML (`fabrica.yaml`) — Viper scoped inside `internal/config` only
- **No logging library:** `fmt.Printf`/`Println` only
- **EC2 stop/start:** uses `cloud.EC2InstanceManager` (auxiliary interface) + EC2 SDK, not Cloud Control

## Module-Specific Notes

### Workstation
- **AMI-first provisioning** — the AMI must already have NICE DCV installed. Fabrica only configures and starts the DCV session via cloud-init.
- **No credentials in UserData** — DCV session password is written to `.fabrica/workstation-credentials.yaml` (mode 0600) only; never embedded in UserData.
- **Port** — 8443 (NICE DCV HTTPS). Default `allowedCidr` is `0.0.0.0/0`; restrict to a VPN CIDR in production via `workstation.allowedCidr` in `fabrica.yaml`.
- **Templates** — `--template artist` → `g6.xlarge` + 200 GiB; `--template programmer` → `c7i.xlarge` + 100 GiB. Precedence is per field: explicit `--instance-type`/`--volume-size` flags > config > template > default (`resolveSizing`).
- **Cost matches the resolved shape** — create-time estimates price the template/flag-resolved shape via `workstation.CostResourcesFor(instanceType, volumeSize)`; `CostResources(cfg)` remains the config-derived fallback for reporting.
- **`--mount-perforce`** — reads the Perforce module's instance private IP from local state via Cloud Control `Get`, then injects `P4PORT=<ip>:1666` into `~/.p4config` via cloud-init. Requires Perforce to be provisioned first.
- **Stop/start state** — stop sets `"stopped"`, start sets `"ready"`. Fire-and-accept; Fabrica does not wait for terminal state.
- **Terminate vs destroy** — uses `terminate` as the permanent deletion command.
- **Idle timeout** — `workstation.idleTimeoutMinutes` in `fabrica.yaml` (default 60) is injected into the DCV cloud-init; the constant `DefaultIdleTimeoutMinutes` lives in `internal/workstation/config.go`.
- **GPU instance prices** — g4dn, g5, g6, and c7i family prices live in `internal/perforce/cost.go`. Do not add a separate cost registration for workstation resources.

### Perforce
- **Version pins rot** — Perforce's jammy archive drops old releases; `helix-p4d=2024.2` no longer exists and apt needs the full `X.Y-build~jammy` string anyway. The userdata falls back to the current repo version when a pin fails; `DefaultHelixVersion` (internal/perforce/config.go) tracks the oldest still-published release.
- **Two packaging generations** — 2026.1 split Helix Core into p4-server packages: `configure-p4d.sh` takes a positional service name with `-P password` (legacy `--super-passwd`/`-y` gone) and instances are managed via **p4dctl**, not a helix-p4d systemd unit. The userdata detects the installed generation at RUNTIME (`--help` grep + unit-file check) — never at render time, because the pin can fall back to a newer generation than requested.
- **Data volume auto-detection is mandatory** — EC2 NVMe enumeration order flips between launches; `/dev/nvme1n1` has pointed at the ROOT disk. Userdata detects the largest non-root unformatted volume at runtime; an explicit `dataDevice` override still wins.
- **Backup auth = login ticket, not P4PASSWD** — modern p4-server security levels ignore/reject bare `P4PASSWD`. The generated script does `p4 login -a` into an isolated `P4TICKETS` file; keep it that way.
- **SSM sessions lack the AWS CLI** — scripts that call `aws s3 …` self-install `awscli` on demand (instance profile supplies credentials).
- **Destroy retains the data volume by design** (DeleteOnTermination=false on /hxdepots); operators delete it when done. It IS tagged, so sweeps see it.

### Horde
- **AMI-first provisioning** — AMI must contain MongoDB 7, Redis 6.2, and the Horde server binary. See `docs/horde-ami.md`.
- **No credentials in UserData** — MongoDB password is written to `.fabrica/horde-credentials.yaml` (mode 0600) only.
- **Ports** — 5000 (HTTP/web UI), 5002 (gRPC for agents). Status probes port 5000 only.
- **Submit URL** — `hordeHTTPClient` uses the instance's private IP from Cloud Control. Requires VPN or same-VPC access; no public IP in V1.
- **`horde_service_token`** in credentials is optional; if empty the auth header is omitted.
- **HTTP timeout** — all Horde API requests carry a 30-second client timeout (`hordeHTTPTimeout`); command contexts cancel sooner on interrupt.
- **Agent SG direction** — agents dial the coordinator, so the agent SG has no inbound rules; a standalone `AWS::EC2::SecurityGroupIngress` on the coordinator SG (source: agent SG) authorizes enrollment. The rule is tracked with `role=agent` and deleted first during teardown.

### Lore
- **AMI-first provisioning** — the AMI must already contain the `loreserver` binary.
- **Runs parallel to Perforce, not instead of it** — no interaction with the `perforce` module.
- **Ports** — 41337 (gRPC/QUIC) and 41339 (HTTP health, `GET /health_check`). Status probes 41339 only.
- **Store path** — local/EBS by default (`DefaultStorePath = /opt/loreserver/store`); `lore.storeBackend: s3` adds a versioned S3 bucket, which destroy purges first via `cloud.S3BucketCleaner` (all versions + delete markers) before Cloud Control deletes the empty bucket.
- **Built on the shared EC2 packages** — `internal/lore` is the reference consumer of `ec2plan`/`ec2state`/`ec2cost`/`userdata`. Use it as the template for a new single-instance module ahead of `internal/perforce`.

### DDC
- **Home + edge regions** — `ddc setup` provisions the home-region host. `ddc region add REGION` provisions a peer-region edge node (SG + EC2 only) reusing the home blob bucket + IAM profile. Edge AMIs are region-specific.
- **Region-scoped clients** — `ddc region add`/`destroy` type-assert the provider to `cloud.RegionProvider` and call `WithRegion(ctx, region)` for a `RegionView{Resources, VPCs}` bound to the target region.
- **Backend choice** — `--backend zen` (default) or `--backend scylla` (adds 1-node Scylla bootstrap, not HA). CQL port 9042 opened only for `scylla`, internal-CIDR only.
- **Hybrid storage** — EBS for local/hot storage plus S3 bucket for cold tier.
- **Endpoints file** — `setup` writes `.fabrica/ddc-endpoints.yaml` instead of a credentials file.
- **Probe** — `GET /health/ready` on the public port; `status` live-probes edge regions (region-scoped Cloud Control + health requests) with the command context threaded through, so Ctrl+C stops in-flight probes.
- **Deferred (Phase 2+)** — replication peers, OIDC/HTTPS, `ddc ami build`, production (HA) Scylla.

### CI
- **Orchestration layer over Horde** — `ci` does not replace Horde; CodeBuild is the conductor, Horde stays the BuildGraph executor.
- **CodeBuild is NOT Cloud Control** — `AWS::CodeBuild::Project` returns `UnsupportedActionException` for CREATE. The project is created/deleted via `cloud.CodeBuildRunner` SDK auxiliary interface; only the IAM role goes through Cloud Control.
- **`ci trigger` semantics** — V1 starts the CodeBuild project directly. The design intends `trigger` to start a CodePipeline execution once CodePipeline orchestration is added (deferred).
- **Idempotency** — `EnsureProject` checks `BatchGetProjects` before creating. `DeleteProject` is idempotent on the AWS side.
- **Tags** — `injectFabricaTags` merges into the capitalized `Tags` array, never a lowercase `tags` key.
- **`ci destroy`** — deletes the CodeBuild project (SDK) then the IAM role (Cloud Control). Has `RunOrchestrated` entry point for `destroy --all`.

### Deploy
- **GameLiftManager SDK split** — fleet activation polling and fleet events retrieval go through `cloud.GameLiftManager` SDK auxiliary interface; Build, Fleet, and Alias creation use Cloud Control.
- **Non-blocking fleet create via CreateFleetAsync** — `promote` starts fleet creation with a non-blocking Cloud Control path, then polls `FleetStatus` for ACTIVE.
- **Alias-flip blue/green** — `promote` always creates a new fleet and flips the alias to it only once ACTIVE. The previous fleet is retained for `rollback`.
- **Retain prior fleet for rollback** — `promote` stores the previous active fleet ID in state so `rollback` can flip the alias back instantly.
- **Incremental state writes are checked** — a failed write after build registration stops promote before fleet creation; under `--no-wait`, a failed post-fleet write is an error, not a false success.
- **Destroy default vs. `--all`** — `destroy` (default) deletes fleets + builds but leaves the alias and IAM role. `--all` also removes the alias + role.
- **`deploy.buildBucket` required** — S3 bucket where CI/Horde uploads packaged builds. `promote` uses `s3://<buildBucket>/builds/<build-version>/server.zip` by default.

### Destroy
- **Teardown order** — `destroy --all`: deploy → ci → workstation → ddc → horde → lore → perforce.
- **Backend-only-on-full-success gate** — state backend deleted only if every module's teardown succeeds.
- **Single aggregate confirmation phrase** — `type "destroy all <account-id>" to continue`.
- **Deploy torn down with `all=true`** — `destroy --all` deletes everything (fleets, builds, alias, IAM role). Plain `fabrica deploy destroy` retains alias + role.
- **CI's SDK-delete special case** — CodeBuild project deleted via SDK, IAM role via Cloud Control. CI uses `cidestroy.RunOrchestrated`.
- **Continue-on-failure + backend preserved** — failed modules are printed with errors; the backend is never deleted on any failure.

### Drift
- **Stopped workstation is in sync** — workstation ships supported stop/start commands, so a parked instance is not drift; stopped Horde/Perforce/Lore/DDC instances still report Mismatch.
- **Terminated instances are always drift** — deletion outside Fabrica must surface.

### Cost
- **Config-derive model** — fully offline; `costsource.Aggregate` engine reads state + config. No AWS calls.
- **Stopped instances drop the compute line** — `workstation stop` filters the instance out of the cost model; EBS volumes remain billed.
- **Deploy fleet cost counted only when a fleet exists** — zero cost until a fleet is promoted.
- **Local thresholds only** — `cost alerts` work entirely on the local `fabrica.yaml` — no AWS Budgets resources. `alerts check` is informational (exit 0 always).

## Planned Command Structure

```
fabrica setup                               # guided first-run provisioning wizard
fabrica status                              # health of all modules
fabrica perforce create|status|destroy|backup|restore  # ✓ implemented
fabrica horde create|status|submit|destroy  # ✓ implemented
fabrica lore create|status|destroy          # ✓ implemented (v0.2; parallel to Perforce)
fabrica ddc setup|status|destroy|region add    # ✓ implemented (home region + edge regions)
fabrica horde ami build                     # ✓ implemented; generates Image Builder recipe + optional Packer HCL
fabrica ci setup|trigger|status|logs|destroy        # ✓ implemented; CodeBuild orchestration over Horde
fabrica deploy setup|promote|rollback|status|destroy  # ✓ implemented; GameLift blue/green deployment
fabrica workstation create|list|stop|start|terminate  # ✓ implemented
fabrica cost report|forecast|alerts         # ✓ implemented; offline cost visibility + local budget alerts
fabrica doctor                              # prerequisite validation
fabrica drift                               # ✓ implemented; read-only drift detection vs live AWS
fabrica mcp                                 # ✓ implemented; stdio MCP server (6 read-only tools)
fabrica destroy --all                       # ✓ implemented; full-stack teardown
fabrica export --format cloudformation      # ✓ implemented; IaC templates from local state
```
