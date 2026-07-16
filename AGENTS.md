# Fabrica — Agent Instructions

## Project Overview

Go CLI that provisions game studio cloud infrastructure on AWS. Single binary, zero external dependencies. Sister tool to [Ludus](https://github.com/jpvelasco/ludus) — Ludus orchestrates game builds, Fabrica gives them somewhere to run.

**Current state:** Phase 0 complete; Phase 1 core complete; Lore (v0.2) complete; DDC V1 (single-region) complete. Modules implemented: `perforce`, `horde`, `lore`, `ddc`, `workstation`, `ci`, `deploy`, and `cost`, plus full-stack `destroy --all` and a CLI E2E test suite. See [ROADMAP.md](ROADMAP.md) and [CLAUDE.md](CLAUDE.md) for the authoritative, current module status — this file is a high-level orientation, not a status mirror.

## Current Modules

| Module | Commands | What it does |
|--------|----------|--------------|
| `perforce` | `create`, `status`, `destroy`, `backup`, `restore` | Provisions a Perforce Helix Core EC2 instance with SG + SSM instance profile; tracks provisioning state; TCP probe on 1666; EBS backup/restore via SSM |
| `horde` | `create`, `status`, `submit`, `destroy`, `ami build` | Provisions an Unreal Horde build coordinator (AMI-first, m7i.2xlarge); probes port 5000; parses BuildGraph XML and POSTs jobs to the Horde REST API; generates EC2 Image Builder recipe + optional Packer HCL for building the required AMI |
| `lore` | `create`, `status`, `destroy` | Provisions an Epic Lore (`loreserver`) EC2 instance (AMI-first, local/EBS store); probes `GET /health_check` on port 41339; parallel to Perforce |
| `ddc` | `setup`, `status`, `destroy` | Provisions Unreal Cloud DDC (Jupiter) on EC2 (AMI-first, single home-region V1); hybrid EBS+S3; default `zen` backend; probes `GET /health/ready` |
| `workstation` | `create`, `list`, `stop`, `start`, `terminate` | Provisions a NICE DCV cloud workstation on EC2 (AMI-first, g4dn.xlarge default); allows TCP 8443 inbound; writes DCV session credentials to `.fabrica/workstation-credentials.yaml`; supports stop/start via EC2InstanceManager and permanent termination |

**Perforce** provisions a Helix Core version control server on EC2 — security group, instance, and credentials — then tracks whether the server is accepting connections. It's the source-of-truth for a game studio's asset and code history.

**Horde** provisions Unreal Engine's build farm coordinator on EC2 and wires it to submit BuildGraph jobs. It expects a pre-baked AMI with MongoDB, Redis, and the Horde binary already installed; Fabrica handles configuration and startup via cloud-init.

## Current Known Limitations

- **State backend is created by `fabrica setup`.** `fabrica setup` provisions the S3 state bucket (versioning + encryption + public-access-block) and the DynamoDB lock table, idempotently — it shows a plan + cost estimate and prompts before any write (`--yes` skips, `--dry-run` previews). Run it once before other commands.
- **Horde requires a user-provided AMI.** `fabrica horde create` is AMI-first: your AMI must already contain MongoDB 7, Redis 6.2, and the Horde server binary. Fabrica does not build or publish this AMI. See [docs/horde-ami.md](docs/horde-ami.md) for requirements.
- **DDC is single-region in V1.** `fabrica ddc setup` provisions one home-region Unreal Cloud DDC instance (no multi-region / `region add`). See [docs/ddc-ami.md](docs/ddc-ami.md).

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
- Coverage: new/changed code must meet the Codecov `patch` gate (≥90%, enforced in CI via `codecov.yml`); no new function ships at 0%
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

## Workstation-Specific Notes

- **Templates** — `--template artist` sets `g6.xlarge` + 200 GiB; `--template programmer` sets `c7i.xlarge` + 100 GiB. Explicitly passing `--instance-type` or `--volume-size` overrides the config but a `--template` overrides config (not the explicit flags — flags win when set after template processing).
- **`--mount-perforce`** — reads the Perforce module's instance private IP from local state via Cloud Control `Get`, then injects `P4PORT=<ip>:1666` into `~/.p4config` via cloud-init. Requires Perforce to be provisioned first. Developer still runs `p4 sync` manually.
- **Stop/start state** — after a successful stop, `ModuleState.Status` is set to `"stopped"`. After a successful start, it is set to `"ready"`. The EC2 API call is fire-and-accept; Fabrica does not wait for the instance to reach a terminal state.
- **Terminate vs destroy** — the workstation module uses `terminate` as the permanent deletion command (not `destroy`). Follows the same ordered-delete + incremental state pattern as `perforce destroy` and `horde destroy`.

## Key Decisions (Locked)

- **Module path:** `github.com/jpvelasco/fabrica`
- **IaC:** AWS Cloud Control API (`aws-sdk-go-v2/service/cloudcontrol`) — no Terraform, no Pulumi
- **State backend:** S3 + DynamoDB (locking) + `.fabrica/state.json` (local cache)
- **Config:** Viper + YAML (`fabrica.yaml`) — Viper scoped inside `internal/config` only
- **No logging library:** `fmt.Printf`/`Println` only
- **EC2 stop/start:** uses `cloud.EC2InstanceManager` (auxiliary interface) + EC2 SDK, not Cloud Control
