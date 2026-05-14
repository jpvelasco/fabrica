# Fabrica Phase 0 — Walking Skeleton

## Context

Fabrica (`F:/source/fabrica/`) is currently spec-only — just `CLAUDE.md` and `Fabrica_PRODUCT_SPEC.md`. The spec locks in major architectural decisions (Go + Cobra + Viper + AWS Cloud Control API; S3 + DynamoDB state backend) and budgets Phase 0 at 2–3 weeks for "project setup, CLI skeleton, AWS foundation."

This plan builds that walking skeleton: `fabrica --version`, `fabrica doctor`, `fabrica setup --dry-run`, and a working S3 + DynamoDB state-backend bootstrap. Zero Perforce/Horde/CI provisioning — those are Phase 1+. The intent is that every Phase 1 module drops into existing directories without any scaffolding refactor, and that the AWS-specific code lives behind a `cloud.Provider` interface so GCP/Azure can slot in later without rewriting callers.

Ludus (`F:/source/ludus/`) is the reference implementation for every convention (flat `main.go`, `cmd/<name>/` subpackages, `internal/<domain>/`, Viper only inside `config.Load()`, diagnostic free-functions, stdlib-only tests, no Makefile). The AWS Cloud Game Dev Toolkit (`F:/source/cloud-game-development-toolkit/`) is the reference architecture for what Phase 1+ Perforce/Horde modules must replicate through Cloud Control API instead of Terraform.

## The constellation (Praetorium)

Fabrica is one piece of a larger empire of tooling — internally referred to as **Praetorium** until the full constellation ships. Each tool is cohesive on its own and composes with the others without tight coupling:

- **Ludus** — Unreal Engine 5 developer workstation tool. The first of the constellation to ship; source of every Go CLI convention in this plan.
- **Fabrica** (this project) — Studio infrastructure provisioner. Stands up Perforce, Horde, CI, cost dashboards, and the shared state backend.
- **Classis** — Cloud-agnostic fleet control tower for game servers. Backend-agnostic by design (GameLift today, Agones/raw EC2/GCE next).
- **Nuntius** (`github.com/jpvelasco/nuntius`) — Dedicated GameLift MCP server. Lets Claude drive fleet operations directly.
- **Vigiles** (future) — Shared intelligence layer: anomaly detection, cost forecasting, diagnostics, predictive scaling. Will consume telemetry from Fabrica and Classis.
- **Praetorium** — Umbrella name for the whole empire. Not a product yet; revealed once the constellation is complete.

**How Fabrica fits:** Fabrica owns the _studio-level infrastructure layer_ of the Praetorium. It provisions the foundational systems (source control, build farms, CI/CD, shared state) that the rest of the empire depends on. Ludus consumes BuildGraph output from Fabrica's Horde. Classis will eventually consume deployment targets and state from Fabrica. Vigiles will consume telemetry and cost data. Nuntius is the dedicated GameLift MCP that Classis will use. The `cloud.Provider` interface introduced in Phase 0 is the same abstraction pattern used by Classis for its backend system — this is how the Praetorium stays cohesive while remaining loosely coupled to any specific cloud.

## Design principles

These govern every Phase 0 structural decision and carry forward to Phase 1+.

1. **High cohesion, loose coupling.** Each `internal/<domain>` package owns one concern and exposes a narrow interface. No package imports a sibling's internals. `cloud`, `state`, `config`, `cost` are independently testable.
2. **CLI-first, MCP-native.** Every capability ships as a Cobra command first; MCP tools (Phase 1+) wrap the same business-logic functions. No command logic lives in `cmd/*` — it lives in `internal/*` and is called from both.
3. **Day-2 is first-class.** `doctor`, `status`, drift detection, and cost reporting are not afterthoughts. Phase 0 lands `doctor` and the `destroy` skeleton before any provisioning feature, because running infra is 90% of the lifecycle.
4. **Clear resource ownership + layered architecture.** Strict one-way dependency flow: `cmd/*` → `internal/<domain>` → `internal/cloud` (+ `internal/state`, `internal/tags`). No domain package imports `cmd/*`; no `internal/cloud` impl imports a sibling domain. This is how the AWS Cloud Game Dev Toolkit's modules accidentally cross-reference each other — Fabrica refuses that failure mode up front.
5. **Cost transparency.** Every mutating operation estimates monthly cost before execution. `setup --dry-run` prints the bill. Phase 1 modules must register a cost estimator or the `plan`/`dry-run` output will warn loudly.
6. **Reconciliation mindset.** Operations are idempotent. `setup` can run N times. State on S3 is canonical; local `.fabrica/state.json` is a cache. Drift between desired and actual is a first-class concept from Phase 0's state schema — even though the drift _command_ is Phase 1+.

**UI strategy.** Fabrica is CLI-first + MCP-native. No web or desktop UI is planned for Phase 0 or Phase 1. Any future unified console (the "Praetorium Console") would be a separate product.

## Decisions (locked in)

- **Module path**: `github.com/jpvelasco/fabrica`
- **Go version**: 1.25.9 (matches Ludus)
- **Cloud provider abstraction**: `internal/cloud.Provider` interface from day one. `internal/cloud/aws` is the first (and only, in Phase 0) implementation. Config gate: `cloud.provider: aws`.
- **Cloud Control wrapper**: `aws.Client` wraps Cloud Control API behind the provider interface; direct-SDK fallback hooks reserved for Phase 1+ resources without Cloud Control coverage.
- **State encryption**: SSE-S3 default; SSE-KMS opt-in via `state.kmsKeyId`
- **`--profile` flag**: selects `fabrica-<name>.yaml` (Fabrica profile). AWS credential profile goes in `cloud.aws.profile:` inside the YAML — two separate concepts
- **Lock style**: Terraform-style single-item DynamoDB lock (PK `LockID`, conditional CAS on `Digest`)
- **Mapstructure tags**: add `mapstructure:` on new config structs (avoid Ludus's implicit fallback technical debt)
- **Cost estimation**: `internal/cost.Estimator` interface in Phase 0, **registered by resource `TypeName`, not by cloud**. Phase 0 ships estimators only for the two resources `setup` creates (`AWS::S3::Bucket`, `AWS::DynamoDB::Table`). Phase 1 modules register their own estimators per resource type. Phase 2+ providers (GCP, Azure, …) register estimators for their native resource type names against the same registry.

## Directory tree

```
F:/source/fabrica/
├── main.go                         # 13-line wrapper → cmd/root.Execute()
├── go.mod / go.sum
├── fabrica.example.yaml            # committed template
├── .golangci.yml                   # copy from F:/source/ludus/.golangci.yml
├── .gitignore                      # fabrica.exe, .fabrica/, fabrica.yaml, coverage.out
├── README.md                       # stub → PRODUCT_SPEC
├── .github/workflows/ci.yml        # mirror Ludus CI (lint + build/test matrix)
├── cmd/
│   ├── root/root.go                # cobra root, PersistentPreRunE
│   ├── globals/globals.go          # Cfg, Verbose, JSONOutput, DryRun, Profile, AssumeYes
│   ├── version/version.go          # `fabrica version`
│   ├── doctor/doctor.go            # diagnostics (Ludus free-function pattern)
│   ├── doctor/doctor_test.go
│   ├── setup/setup.go              # Phase 0: detect → estimate cost → confirm → state.Bootstrap()
│   ├── destroy/destroy.go          # stub + --all + confirm prompt wired for Phase 1
│   └── configcmd/configcmd.go      # `fabrica config show`
└── internal/
    ├── version/version.go          # Version, Commit (ldflags targets)
    ├── config/config.go            # Config + sub-structs, Load, Defaults, Clone
    ├── config/config_test.go
    ├── cloud/
    │   ├── provider.go             # Provider interface (Identity, Resources, State backend factories)
    │   ├── registry.go             # Register(name, factory) + Get(name) — mirrors Classis Backend registry
    │   └── aws/
    │       ├── aws.go              # awsProvider implements cloud.Provider; registered via init()
    │       ├── config.go           # LoadAWSConfig(ctx, region, profile)
    │       ├── identity.go         # CallerIdentity(ctx, cfg)
    │       ├── cloudcontrol.go     # Cloud Control wrapper (Create/Get/Update/Delete/List)
    │       ├── resource.go         # Resource{TypeName, Identifier, DesiredState}
    │       ├── poll.go             # WaitForRequest, exponential backoff
    │       ├── tags.go             # InjectFabricaTags into DesiredState JSON
    │       └── aws_test.go
    ├── state/
    │   ├── bootstrap.go            # S3 + DynamoDB via direct SDK (chicken-and-egg)
    │   ├── state.go                # State schema + Load/Save (S3 + local cache)
    │   ├── lock.go                 # DynamoDB Acquire/Release (conditional CAS)
    │   └── state_test.go
    ├── cost/
    │   ├── estimator.go            # Estimator interface + Registry keyed by resource TypeName (provider-agnostic)
    │   ├── estimators_phase0.go    # Phase 0 built-ins: AWS::S3::Bucket, AWS::DynamoDB::Table. GCP/Azure register their own in Phase 2+.
    │   └── cost_test.go
    ├── tags/tags.go                # Standard(module, version) — single source of truth
    └── prompt/prompt.go            # Confirm(msg) — stdlib, tty-aware
```

**Dependency direction (enforced by review, checked by `go list`):**
```
cmd/*  →  internal/{config, state, cost, tags, prompt, cloud}
                                                      ↓
                                            internal/cloud/aws (only impl)
```
`internal/cloud/*` never imports `internal/state`, `internal/cost`, or any `cmd/*`. `internal/state` uses `cloud.Provider` through its interface, not `cloud/aws` directly.

## `go.mod`

```
module github.com/jpvelasco/fabrica

go 1.25.9

require (
    github.com/aws/aws-sdk-go-v2                        v1.41.x
    github.com/aws/aws-sdk-go-v2/config                 v1.32.x
    github.com/aws/aws-sdk-go-v2/service/cloudcontrol   v1.x.x
    github.com/aws/aws-sdk-go-v2/service/s3             v1.100.x
    github.com/aws/aws-sdk-go-v2/service/dynamodb       v1.x.x
    github.com/aws/aws-sdk-go-v2/service/sts            v1.42.x
    github.com/aws/aws-sdk-go-v2/service/iam            v1.53.x   // for doctor perms sanity
    github.com/spf13/cobra                              v1.10.2
    github.com/spf13/viper                              v1.21.0
)
```

Match Ludus pins where they overlap. No `cloudformation`, `ecr`, `gamelift` yet — Phase 1.

## `fabrica.yaml` schema (Phase 0 fields only)

```yaml
cloud:
  provider: aws               # only "aws" in Phase 0; gcp/azure Phase 2+
  aws:
    region: us-east-1
    profile: ""               # empty = SDK default credential chain
    accountId: ""             # auto-detected on first `setup`, cached
    tags: {}                  # merged with fabrica-standard tags

state:
  bucket: ""                  # default: fabrica-state-<account-id>
  table:  fabrica-state-lock
  kmsKeyId: ""                # empty = SSE-S3; set for SSE-KMS

# Empty sub-structs exist so Phase 1 can add fields without restructuring:
perforce: {}
horde: {}
ci: {}
cost: {}                      # thresholds, budget alerts — Phase 1+
```

## Key patterns (reuse from Ludus + Classis)

- **Flat `main.go`** → calls `cmd/root.Execute()`: `F:/source/ludus/main.go`
- **Root command**: `F:/source/ludus/cmd/root/root.go` — `PersistentPreRunE`, persistent flags, `signal.NotifyContext`
- **Config loader**: `F:/source/ludus/internal/config/config.go` — Viper scoped to `Load()`, profile precedence, project-local only
- **Provider registry pattern**: Classis's `Backend` interface + `init()` registration. `cloud.Register("aws", ...)` in `internal/cloud/aws/aws.go` — mirror that shape.
- **Doctor**: `F:/source/ludus/cmd/doctor/doctor.go` — local `diagnostic` struct, free-function checks, no interface registry
- **Golangci config**: copy `F:/source/ludus/.golangci.yml` verbatim
- **CI workflow**: mirror `F:/source/ludus/.github/workflows/ci.yml`

## File-by-file purpose

| File | Purpose |
|------|---------|
| `main.go` | `os.Exit(1)` wrapper around `root.Execute()` — ~13 lines, Ludus-identical |
| `cmd/root/root.go` | Root cobra cmd, persistent flags (`--config`, `--verbose`, `--json`, `--dry-run`, `--profile`, `--yes`), `PersistentPreRunE` loads config, resolves `cloud.Provider` from `cloud.provider`, populates `globals.Cfg` |
| `cmd/globals/globals.go` | Shared `Cfg *config.Config`, `Provider cloud.Provider`, `Verbose`, `JSONOutput`, `DryRun`, `Profile`, `AssumeYes` |
| `cmd/version/version.go` | Prints `version.Version`, `version.Commit`, Go version, OS/arch |
| `cmd/doctor/doctor.go` | `diagnostic` struct + free-function checks + `printDiagnostics` formatter |
| `cmd/setup/setup.go` | Detect creds → detect account → **estimate monthly cost via `cost.Registry`** → confirm → `state.Bootstrap()` → write/update `fabrica.yaml` with account ID → print next-steps. `--dry-run` prints plan + cost estimate; no AWS mutation |
| `cmd/destroy/destroy.go` | Prints Phase 0 stub message; wires `--all`, `--yes`, `prompt.Confirm()` so Phase 1 drops in |
| `cmd/configcmd/configcmd.go` | `fabrica config show` — dump loaded config as YAML |
| `internal/version/version.go` | `var Version = "dev"`; `var Commit = "unknown"` — ldflags targets |
| `internal/config/config.go` | `Config` struct + typed sub-structs (Cloud, State, Perforce, Horde, CI, Cost); `Load(path)`, `Defaults()`, `Clone()`. Viper scoped here only |
| `internal/cloud/provider.go` | `Provider` interface: `Identity(ctx) (account, arn, region, error)`, `Resources() ResourceClient`, `Name() string`. `ResourceClient` is the narrow surface every cloud must implement (Create/Get/Update/Delete/List on `Resource{TypeName, Identifier, DesiredState}`) |
| `internal/cloud/registry.go` | `Register(name, factory)`, `Get(name, cfg) (Provider, error)`. Imported by `cmd/root` to resolve provider from config |
| `internal/cloud/aws/aws.go` | `awsProvider` struct implements `cloud.Provider`; registers itself via `init()` |
| `internal/cloud/aws/config.go` | `LoadAWSConfig(ctx, region, profile) (aws.Config, error)` |
| `internal/cloud/aws/identity.go` | `CallerIdentity(ctx, cfg) (account, arn, region string, err error)` wrapping `sts:GetCallerIdentity` |
| `internal/cloud/aws/cloudcontrol.go` | `awsResourceClient` wraps Cloud Control API; returned by `awsProvider.Resources()` |
| `internal/cloud/aws/resource.go` | `Resource{TypeName, Identifier string; DesiredState json.RawMessage}` — shared shape |
| `internal/cloud/aws/poll.go` | `WaitForRequest(ctx, token, timeout)` — exponential backoff on `GetResourceRequestStatus` |
| `internal/cloud/aws/tags.go` | `InjectFabricaTags(state, module, version, extra)` — merges standard tags into `DesiredState.Tags` |
| `internal/state/bootstrap.go` | `Bootstrap(ctx, provider, cfg)` — **fully idempotent**: each of the six AWS API calls (bucket create, versioning, public-access block, encryption, DynamoDB create, wait-for-active) handles `AlreadyExists`/`ResourceInUse` as success and prints a clear `already exists — skipping` line per resource. Creates S3 bucket (versioning, block-public-access, SSE-S3 default, SSE-KMS if `kmsKeyId` set) + DynamoDB table (PK `LockID`, PAY_PER_REQUEST). Takes `cloud.Provider` so non-AWS backends can diverge later. Second run must be a no-op with zero errors and a clean summary. **Vigiles will later consume state and cost telemetry from this layer.** |
| `internal/state/state.go` | `State{Resources, History, Modules}` schema; `Load/Save` with S3 canonical + `.fabrica/state.json` local cache |
| `internal/state/lock.go` | `Acquire(ctx, id, holder) (token, error)`, `Release(ctx, id, token)` via DynamoDB conditional puts |
| `internal/cost/estimator.go` | `Estimator` interface: `Estimate(resource Resource) (Monthly, error)`. `Registry` keyed by resource `TypeName` — **provider-agnostic**; any provider (AWS today, GCP/Azure in Phase 2+) registers estimators against the same registry. `Monthly` carries USD amount + confidence flag. Used by `setup --dry-run` and Phase 1 `plan`. **Vigiles will later consume cost telemetry and diagnostics from this layer.** |
| `internal/cost/estimators_phase0.go` | Two estimators registered in Phase 0: `AWS::S3::Bucket` (flat $0.023/GB-month storage + request tier warning), `AWS::DynamoDB::Table` (on-demand; near-zero for idle lock table). Phase 1+ modules and Phase 2+ providers register their own estimators the same way |
| `internal/tags/tags.go` | `Standard(module, version) map[string]string` → `{ManagedBy: fabrica, FabricaModule, FabricaVersion}` |
| `internal/prompt/prompt.go` | `Confirm(msg) bool` — stdin reader, honors `--yes` flag via context |

## Phase 0 scope (tightened)

**In:** scaffolding, config, `cloud.Provider` + AWS impl, basic state schema + bootstrap, `doctor` (5 checks: Go version, AWS creds, region, Fabrica version, state backend reachable), `setup` (with cost estimation), `destroy` skeleton, `version`, `config show`, CI, lint.

**Deferred to Phase 1 (keeps Phase 0 focused on skeleton):**
- Deep IAM permissions simulation in `doctor` (just check creds resolve + region set; drop `iam:SimulatePrincipalPolicy`)
- State `Load/Save` only needs the schema + S3 read/write. Conflict resolution, history pagination, module-index queries → Phase 1
- `doctor` post-setup checks (bucket-exists, table-exists) stay; deep bucket-policy validation → Phase 1

## Order of implementation (always compiles)

1. `go mod init` + pinned `go get` + `main.go` + empty `cmd/root/root.go` with one `Cmd`. **Compiles, does nothing.**
2. `internal/version` + `cmd/version` wired into root. `fabrica --version` works.
3. `internal/config` (all sub-structs incl. `Cloud{Provider, AWS}`, `Defaults()`, `Load()`, mapstructure tags) + `cmd/globals` + `PersistentPreRunE`. Config loads, flags wired.
4. `internal/cloud/provider.go` + `internal/cloud/registry.go` — interface + registry only, no impls. Compiles with empty registry.
5. `internal/cloud/aws/{config,identity,resource,poll,tags,cloudcontrol,aws}.go` — concrete `awsProvider` registers via `init()`. Unit tests (`tags_test.go`, `aws_test.go` against mocked SDK interfaces) immediately.
6. `internal/tags`. Root resolves `cloud.Get(cfg.Cloud.Provider, cfg)` and stashes the `Provider` in `globals`.
7. `cmd/doctor` with 5 checks: Go version, AWS creds (via `Provider.Identity`), region, Fabrica version, **state backend reachable**. Implementation: lightweight non-mutating probe using `HeadBucket` + `DescribeTable` with 2–3s timeout per call. Missing bucket/table → `[WARN] State backend not yet provisioned (run fabrica setup)`. Present but unhealthy (403, throttled, wrong region) → `[ERROR]` with the underlying cause. Present and healthy → `[OK]`. The 5th check is a Day-2-first-class anchor: operators should know at any time whether the state backend is healthy, pre- and post-setup.
8. `internal/state/{lock,state,bootstrap}.go`. Bootstrap takes `cloud.Provider` + `config.Config`. Unit-test schema + lock happy-path against mocked SDK interfaces.
9. `internal/cost/{estimator,estimators_phase0}.go` + test. Registry keyed by resource `TypeName`, populated via `init()` for `AWS::S3::Bucket` + `AWS::DynamoDB::Table`.
10. `cmd/setup` — dry-run path first (calls `cost.Registry.Estimate` for planned resources, prints cost table), then real bootstrap path.
11. `cmd/destroy` stub + confirmation wiring.
12. `cmd/configcmd`, then post-setup doctor checks (bucket-exists, table-exists).
13. `.golangci.yml`, `.github/workflows/ci.yml`, `fabrica.example.yaml`, `README.md`.

`go build ./...` and `go test ./...` stay green at every step.

## Verification

**Build and quality gates:**
- `go build ./...` — clean on windows/linux/macos
- `go vet ./...` — clean
- `go test ./... -race -cover` — coverage ≥ 60% for `internal/*`; unit tests use mocked SDK interfaces, no AWS calls
- `golangci-lint run` — clean under Ludus-copied `.golangci.yml`
- **Layering check**: `go list -deps ./internal/cloud/...` must not contain `internal/state`, `internal/cost`, or `cmd/*`

**Runtime smoke tests (against a real AWS account):**
- `./fabrica --version` → `dev` or ldflags-injected semver
- `./fabrica version` → version + commit + Go version + OS/arch
- `./fabrica doctor` → `[OK]` for creds/region/Go/Fabrica, `[WARN]` for state backend pre-setup, exit 0
- `./fabrica setup --dry-run` → prints account ID, proposed bucket/table names, proposed tags, and monthly cost estimate (~$0 for empty bucket + idle lock table, with confidence flag); no AWS mutation
- `./fabrica setup` → creates bucket + table, writes account ID into `fabrica.yaml`, prints next-steps
- `./fabrica setup` (second run) → fully idempotent: exit 0, one `already exists — skipping` line per pre-existing resource, no errors, no stack traces. No duplicate resources created — verified via `aws s3api list-buckets` and `aws dynamodb list-tables`
- `./fabrica doctor` (post-setup) → all `[OK]`
- `./fabrica config show` → dumps loaded config as YAML
- `./fabrica destroy --all` → confirmation prompt, then Phase 0 stub message

## Explicitly NOT in Phase 0 (defer to Phase 1+)

- Perforce Helix Core provisioning
- Horde coordinator + agent provisioning
- BuildGraph XML ingestion
- `fabrica status`, `fabrica cost`, `fabrica promote`, `fabrica ci *`
- MCP server (Phase 1+)
- Module registry / plugin system
- Second cloud provider implementation (GCP/Azure) — interface only in Phase 0
- State encryption key rotation
- Multi-region state
- Resource destroy logic (only the command skeleton)
- Drift detection
- `--fix` auto-remediation
- Deep IAM permissions simulation in `doctor`
- Cost estimators beyond the two Phase 0 resources
