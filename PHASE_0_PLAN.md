# Fabrica Phase 0 — Walking Skeleton

## Context

Fabrica (`F:/source/fabrica/`) is currently spec-only — just `CLAUDE.md` and `Fabrica_PRODUCT_SPEC.md`. The spec locks in major architectural decisions (Go + Cobra + Viper + AWS Cloud Control API; S3 + DynamoDB state backend) and budgets Phase 0 at 2–3 weeks for "project setup, CLI skeleton, AWS foundation."

This plan builds that walking skeleton: `fabrica --version`, `fabrica doctor`, `fabrica setup --dry-run`, and a working S3 + DynamoDB state-backend bootstrap. Zero Perforce/Horde/CI provisioning — those are Phase 1+. The intent is that every Phase 1 module drops into existing directories without any scaffolding refactor.

Ludus (`F:/source/ludus/`) is the reference implementation for every convention (flat `main.go`, `cmd/<name>/` subpackages, `internal/<domain>/`, Viper only inside `config.Load()`, diagnostic free-functions, stdlib-only tests, no Makefile). The AWS Cloud Game Dev Toolkit (`F:/source/cloud-game-development-toolkit/`) is the reference architecture for what Phase 1+ Perforce/Horde modules must replicate through Cloud Control API instead of Terraform.

## Decisions (locked in)

- **Module path**: `github.com/devrecon/fabrica` (matches Ludus)
- **Go version**: 1.25.9 (matches Ludus)
- **Cloud Control wrapper**: Go interface in Phase 0 (enables direct-SDK fallback in Phase 1+ for resources without Cloud Control coverage)
- **State encryption**: SSE-S3 default; SSE-KMS opt-in via `state.kmsKeyId`
- **`--profile` flag**: selects `fabrica-<name>.yaml` (Fabrica profile). AWS credential profile goes in `aws.profile:` inside the YAML — two separate concepts
- **Lock style**: Terraform-style single-item DynamoDB lock (PK `LockID`, conditional CAS on `Digest`)
- **Mapstructure tags**: add `mapstructure:` on new config structs (avoid Ludus's implicit fallback technical debt)

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
│   ├── globals/globals.go          # Cfg, Verbose, JSONOutput, DryRun, Profile
│   ├── version/version.go          # `fabrica version`
│   ├── doctor/doctor.go            # diagnostics (Ludus free-function pattern)
│   ├── doctor/doctor_test.go
│   ├── setup/setup.go              # Phase 0: detect → confirm → state.Bootstrap()
│   ├── destroy/destroy.go          # stub + --all + confirm prompt wired for Phase 1
│   └── configcmd/configcmd.go      # `fabrica config show`
└── internal/
    ├── version/version.go          # Version, Commit (ldflags targets)
    ├── awsutil/config.go           # LoadAWSConfig(ctx, region, profile)
    ├── awsutil/identity.go         # CallerIdentity(ctx, cfg)
    ├── config/config.go            # Config, Load, Defaults, Clone (+ empty Phase 1 sub-structs)
    ├── config/config_test.go
    ├── cloudcontrol/client.go      # Client interface + concrete impl
    ├── cloudcontrol/resource.go    # Resource{TypeName, Identifier, DesiredState}
    ├── cloudcontrol/poll.go        # WaitForRequest, exponential backoff
    ├── cloudcontrol/tags.go        # InjectFabricaTags into DesiredState JSON
    ├── cloudcontrol/client_test.go
    ├── state/bootstrap.go          # S3 + DynamoDB via direct SDK (chicken-and-egg)
    ├── state/state.go              # State schema + Load/Save (S3 + local cache)
    ├── state/lock.go               # DynamoDB Acquire/Release (conditional CAS)
    ├── state/state_test.go
    ├── tags/tags.go                # Standard(module, version) — single source of truth
    └── prompt/prompt.go            # Confirm(msg) — stdlib, tty-aware
```

## `go.mod`

```
module github.com/devrecon/fabrica

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
aws:
  region: us-east-1
  profile: ""              # empty = SDK default credential chain
  accountId: ""            # auto-detected on first `setup`, cached
  tags: {}                 # merged with fabrica-standard tags

state:
  bucket: ""               # default: fabrica-state-<account-id>
  table:  fabrica-state-lock
  kmsKeyId: ""             # empty = SSE-S3; set for SSE-KMS

# Empty sub-structs exist so Phase 1 can add fields without restructuring:
perforce: {}
horde: {}
ci: {}
cost: {}
```

## Key patterns (reuse from Ludus)

- **Flat `main.go`** → calls `cmd/root.Execute()`: `F:/source/ludus/main.go`
- **Root command**: `F:/source/ludus/cmd/root/root.go` — `PersistentPreRunE`, persistent flags, `signal.NotifyContext`
- **Config loader**: `F:/source/ludus/internal/config/config.go` — Viper scoped to `Load()`, profile precedence, project-local only
- **AWS factory**: `F:/source/ludus/internal/awsutil/config.go` — `LoadAWSConfig(ctx, region)`. Extend with `profile` param
- **Doctor**: `F:/source/ludus/cmd/doctor/doctor.go` — local `diagnostic` struct, free-function checks, no interface registry
- **Golangci config**: copy `F:/source/ludus/.golangci.yml` verbatim
- **CI workflow**: mirror `F:/source/ludus/.github/workflows/ci.yml`

## File-by-file purpose

| File | Purpose |
|------|---------|
| `main.go` | `os.Exit(1)` wrapper around `root.Execute()` — ~13 lines, Ludus-identical |
| `cmd/root/root.go` | Root cobra cmd, persistent flags (`--config`, `--verbose`, `--json`, `--dry-run`, `--profile`, `--yes`), `PersistentPreRunE` loads config + populates `globals.Cfg` |
| `cmd/globals/globals.go` | Shared `Cfg *config.Config`, `Verbose`, `JSONOutput`, `DryRun`, `Profile`, `AssumeYes` |
| `cmd/version/version.go` | Prints `version.Version`, `version.Commit`, Go version, OS/arch |
| `cmd/doctor/doctor.go` | `diagnostic` struct + free-function checks + `printDiagnostics` formatter |
| `cmd/setup/setup.go` | Detect creds → detect account → confirm → `state.Bootstrap()` → write/update `fabrica.yaml` with account ID → print next-steps. `--dry-run` prints plan only |
| `cmd/destroy/destroy.go` | Prints Phase 0 stub message; wires `--all`, `--yes`, `prompt.Confirm()` so Phase 1 drops in |
| `cmd/configcmd/configcmd.go` | `fabrica config show` — dump loaded config as YAML |
| `internal/version/version.go` | `var Version = "dev"`; `var Commit = "unknown"` — ldflags targets |
| `internal/config/config.go` | `Config` struct + typed sub-structs (AWS, State, Perforce, Horde, CI, Cost); `Load(path)`, `Defaults()`, `Clone()`. Viper scoped here only |
| `internal/awsutil/config.go` | `LoadAWSConfig(ctx, region, profile) (aws.Config, error)` |
| `internal/awsutil/identity.go` | `CallerIdentity(ctx, cfg) (account, arn, region string, err error)` wrapping `sts:GetCallerIdentity` |
| `internal/cloudcontrol/client.go` | `Client` **interface** with `Create/Get/Update/Delete/List`; concrete `awsClient` impl wraps `cloudcontrol.Client` |
| `internal/cloudcontrol/resource.go` | `Resource{TypeName, Identifier string; DesiredState json.RawMessage}` |
| `internal/cloudcontrol/poll.go` | `WaitForRequest(ctx, token, timeout)` — exponential backoff on `GetResourceRequestStatus` |
| `internal/cloudcontrol/tags.go` | `InjectFabricaTags(state, module, version, extra)` — merges standard tags into `DesiredState.Tags` |
| `internal/state/bootstrap.go` | `Bootstrap(ctx, cfg)` — idempotent creation of S3 bucket (versioning, block-public-access, SSE-S3 by default, SSE-KMS if `kmsKeyId` set) and DynamoDB table (PK `LockID`, PAY_PER_REQUEST) via direct SDK |
| `internal/state/state.go` | `State{Resources, History, Modules}` schema; `Load/Save` with S3 canonical + `.fabrica/state.json` local cache |
| `internal/state/lock.go` | `Acquire(ctx, id, holder) (token, error)`, `Release(ctx, id, token)` via DynamoDB conditional puts |
| `internal/tags/tags.go` | `Standard(module, version) map[string]string` → `{ManagedBy: fabrica, FabricaModule, FabricaVersion}` |
| `internal/prompt/prompt.go` | `Confirm(msg) bool` — stdin reader, honors `--yes` flag via context |

## Order of implementation (always compiles)

1. `go mod init` + pinned `go get` + `main.go` + empty `cmd/root/root.go` with one `Cmd`. **Compiles, does nothing.**
2. `internal/version` + `cmd/version` wired into root. `fabrica --version` works.
3. `internal/config` (all sub-structs, `Defaults()`, `Load()`, mapstructure tags) + `cmd/globals` + `PersistentPreRunE`. Config loads, flags wired.
4. `internal/awsutil/{config,identity}.go`. AWS SDK reachable.
5. `cmd/doctor` with three checks: version, AWS creds, region. End-to-end doctor run works.
6. `internal/tags` + `internal/cloudcontrol/{resource,tags,poll,client}.go` — interface first, concrete impl second. Unit tests (`tags_test.go`, `client_test.go`) immediately.
7. `internal/state/{lock,state,bootstrap}.go`. Unit-test schema + lock happy-path against mocked SDK interfaces.
8. `cmd/setup` — dry-run path first, then real bootstrap path.
9. `cmd/destroy` stub + confirmation wiring.
10. `cmd/configcmd`, then remaining doctor checks (bucket-exists, table-exists, IAM sanity).
11. `.golangci.yml`, `.github/workflows/ci.yml`, `fabrica.example.yaml`, `README.md`.

`go build ./...` and `go test ./...` stay green at every step.

## Verification

**Build and quality gates:**
- `go build ./...` — clean on windows/linux/macos
- `go vet ./...` — clean
- `go test ./... -race -cover` — coverage ≥ 60% for `internal/*`; unit tests use mocked SDK interfaces, no AWS calls
- `golangci-lint run` — clean under Ludus-copied `.golangci.yml`

**Runtime smoke tests (against a real AWS account):**
- `./fabrica --version` → `dev` or ldflags-injected semver
- `./fabrica version` → version + commit + Go version + OS/arch
- `./fabrica doctor` → `[OK]` for creds/region/Go/Fabrica, `[WARN]` for state backend pre-setup, exit 0
- `./fabrica setup --dry-run` → prints account ID, proposed bucket/table names, proposed tags; no AWS mutation
- `./fabrica setup` → creates bucket + table, writes account ID into `fabrica.yaml`, prints next-steps
- `./fabrica setup` (second run) → idempotent, reports `already exists`
- `./fabrica doctor` (post-setup) → all `[OK]`
- `./fabrica config show` → dumps loaded config as YAML
- `./fabrica destroy --all` → confirmation prompt, then Phase 0 stub message

## Explicitly NOT in Phase 0 (defer to Phase 1+)

- Perforce Helix Core provisioning
- Horde coordinator + agent provisioning
- BuildGraph XML ingestion
- `fabrica status`, `fabrica cost`, `fabrica promote`, `fabrica ci *`
- MCP server
- Module registry / plugin system
- State encryption key rotation
- Multi-region state
- Resource destroy logic (only the command skeleton)
- Drift detection
- `--fix` auto-remediation
