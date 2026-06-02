# Fabrica — Agent Instructions

## Project Overview

Go CLI that provisions game studio cloud infrastructure on AWS. Single binary, zero external dependencies. Sister tool to [Ludus](https://github.com/jpvelasco/ludus) — Ludus orchestrates game builds, Fabrica gives them somewhere to run.

**Current state:** Phase 0 (CLI skeleton + AWS foundation) complete. Three modules fully implemented: `perforce` (Helix Core provisioning), `horde` (build farm provisioning + job submission), and `workstation` (NICE DCV cloud workstation provisioning).

## Current Modules

| Module | Commands | What it does |
|--------|----------|--------------|
| `perforce` | `create`, `status`, `destroy` | Provisions a Perforce Helix Core EC2 instance with security group; tracks provisioning state; detects readiness via TCP probe on port 1666 |
| `horde` | `create`, `status`, `submit`, `destroy`, `ami build` | Provisions an Unreal Horde build coordinator (AMI-first, m7i.2xlarge); probes port 5000; parses BuildGraph XML and POSTs jobs to the Horde REST API; generates EC2 Image Builder recipe + optional Packer HCL for building the required AMI |
| `workstation` | `create`, `list` | Provisions a NICE DCV cloud workstation on EC2 (AMI-first, g4dn.xlarge default); allows TCP 8443 inbound; writes DCV session credentials to `.fabrica/workstation-credentials.yaml` |

**Perforce** provisions a Helix Core version control server on EC2 — security group, instance, and credentials — then tracks whether the server is accepting connections. It's the source-of-truth for a game studio's asset and code history.

**Horde** provisions Unreal Engine's build farm coordinator on EC2 and wires it to submit BuildGraph jobs. It expects a pre-baked AMI with MongoDB, Redis, and the Horde binary already installed; Fabrica handles configuration and startup via cloud-init.

## Current Known Limitations

- **`fabrica setup` is not yet functional.** The S3 bucket and DynamoDB lock table must be created manually before using any other Fabrica commands. Running `fabrica setup` without `--dry-run` prints a warning and exits — it does not create any AWS resources. See [docs/setup-manual.md](docs/setup-manual.md) once that document exists, or create the resources manually.
- **Horde requires a user-provided AMI.** `fabrica horde create` is AMI-first: your AMI must already contain MongoDB 7, Redis 6.2, and the Horde server binary. Fabrica does not build or publish this AMI. See [docs/horde-ami.md](docs/horde-ami.md) for requirements.

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

**SDK-free `internal/*`** — `internal/perforce` and `internal/horde` are pure plan layers with no AWS SDK imports. They build `CreatePlan` structs and Cloud Control desired-state JSON. The `cmd/<module>` layer calls the plan layer, then executes via `rt.Provider.Resources()`.

**Seam injection for testability** — the `command` struct holds `func` fields for all I/O operations (`readState`, `writeState`, `createResource`, `probeTCP`, etc.). `New()` wires real implementations; tests inject fakes. No global state, no `init()` side effects in tests.

**Two-package test pattern:**
- `*_test.go` (`package <cmd>`) — white-box tests calling `command.run()` directly with injected seams
- `cobra_test.go` (`package <cmd>_test`) — black-box Cobra-layer tests calling `cmd.New(...).ExecuteContext()`

**Incremental state** — state is written after each resource creation. Partial failures leave a recoverable record; re-running detects already-provisioned resources and exits cleanly.

**VPCResolver interface** — when a module needs AWS-specific resolution, define an interface in `internal/<module>/config.go` that the provider implements. Keeps `internal/*` SDK-free.

**Embedded templates** — `cmd/horde/ami` ships build artifacts as `embed.FS` templates rendered with `text/template`. New file-generator commands should follow this pattern: templates under `cmd/<cmd>/templates/`, rendered via a `renderTemplate` helper on the command struct. No `RuntimeSource`/`OptionsSource` needed when the command makes no AWS calls.

## How to Add a New Command / Module

1. **Create `internal/<module>/`** — pure plan layer: `CreatePlan` struct, Cloud Control desired-state JSON builders, cloud-init generator, cost estimators. No AWS SDK imports.
2. **Create `cmd/<module>/`** — Cobra command wired with `RuntimeSource` + `OptionsSource` closures (see `cmd/perforce/` or `cmd/horde/` as templates).
3. **Add config struct** to `internal/config/config.go` (not inside `internal/<module>/`) to avoid circular imports. Add `mapstructure:` tags.
4. **Register cost estimators** in the plan layer via `cost.Global.Register`. Do NOT register `AWS::EC2::Instance` or `AWS::EC2::Volume` from a second package — they're already registered.
5. **Wire the parent command** in `cmd/root/root.go`.
6. **Tests:** follow the two-file pattern. Cover partial failures, seam errors, confirmation rejection, `--dry-run`, `--json`.

Reference: `cmd/perforce/` and `internal/perforce/` are the canonical templates.

## Important Conventions

**Naming:**
- Packages: lowercase single-word (`perforce`, `horde`, `state`)
- Files: `snake_case.go`
- Acronyms fully uppercase: `ID`, `ARN`, `URL`, `AWS`, `IAM`
- `New*` constructors return pointers; single-letter receivers

**Imports:** stdlib group, blank line, then everything else. `gofmt` only — no goimports or gofumpt.

**Config structs:** always add `mapstructure:` tags. Live in `internal/config/config.go`.

**Error handling:** `fmt.Errorf("context: %w", err)`. Messages state what went wrong AND what to do. No sentinel errors.

**State:** always written after each resource so partial runs are recoverable.

**No logging library:** `fmt.Printf`/`Println` only.

**Cost estimation:** every new resource type needs a cost estimator registered by `TypeName`.

**Tests:**
- No real AWS calls — mock SDK interfaces
- Coverage target: 60%+ for `internal/*`
- Use `GenerateRaw` variants for testing base64-encoded outputs (e.g., cloud-init)
- `cobra_test.go` must build a minimal root command to replicate the persistent-flag hierarchy (`--dry-run`, `--yes`, `--json` live on root)

## Useful Commands

```bash
# Build
go build ./...

# Test (Windows — no -race)
go test ./...

# Test (Linux/macOS — with race detector)
go test -race -coverprofile=coverage.out -covermode=atomic ./...

# Single test
go test ./... -run TestName

# Lint
golangci-lint run ./...

# Coverage summary
go tool cover -func=coverage.out

# Format
gofmt -w .

# Layering check (must not contain internal/state, internal/cost, or cmd/*)
go list -deps ./internal/cloud/...
```

## Key Decisions (Locked)

- **Module path:** `github.com/jpvelasco/fabrica`
- **IaC:** AWS Cloud Control API (`aws-sdk-go-v2/service/cloudcontrol`) — no Terraform, no Pulumi
- **State backend:** S3 + DynamoDB (locking) + `.fabrica/state.json` (local cache)
- **Config:** Viper + YAML (`fabrica.yaml`) — Viper scoped inside `internal/config` only
- **No logging library:** `fmt.Printf`/`Println` only
