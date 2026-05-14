# Fabrica — Agent Instructions

## What This Is

Go CLI that provisions game studio cloud infrastructure on AWS (Perforce, Horde build farms, CI/CD). Sister tool to [Ludus](https://github.com/jpvelasco/ludus). Single binary, zero external dependencies.

**Current state:** Pre-implementation. No code exists yet. Phase 0 (CLI skeleton + AWS foundation) is the next step. See `PHASE_0_PLAN.md` for the implementation order and `Fabrica_PRODUCT_SPEC.md` for full product specs.

## Key Decisions (Locked In)

- **Module path:** `github.com/jpvelasco/fabrica`
- **Go version:** 1.25.9
- **IaC:** AWS Cloud Control API (`aws-sdk-go-v2/service/cloudcontrol`) — no Terraform, no Pulumi
- **State backend:** S3 (canonical) + DynamoDB (locking) + `.fabrica/state.json` (local cache)
- **Config:** Viper + YAML (`fabrica.yaml`) — Viper scoped inside `internal/config` only
- **No logging library:** `fmt.Printf`/`Println` only

## Architecture

Strict one-way dependency flow:

```
cmd/* → internal/{config, state, cost, tags, prompt, cloud}
                                            ↓
                                    internal/cloud/aws (only impl)
```

`internal/cloud/*` never imports `internal/state`, `internal/cost`, or any `cmd/*`. `internal/state` uses `cloud.Provider` through its interface, not `cloud/aws` directly.

## Implementation Order (Phase 0)

Follow the sequence in `PHASE_0_PLAN.md` — 13 steps, each compiles. Do NOT skip ahead. The plan is the authoritative build order; `PHASE_0_PLAN.md` contains the per-file spec.

## Conventions (Mirror Ludus)

All conventions from Ludus apply unless noted otherwise. Reference `F:/source/ludus/AGENTS.md` for:

- Import grouping (stdlib, blank line, everything else)
- Naming (lowercase single-word packages, `snake_case.go` files, uppercase acronyms)
- `New*` constructors returning pointers, single-letter receivers
- `context.Context` as first param for I/O methods
- Table-driven tests, stdlib-only assertions, same-package tests
- Error wrapping: `fmt.Errorf("context: %w", err)`, no sentinel errors

**Fabrica-specific additions:**
- Add `mapstructure:` tags on new config structs (Ludus owes this technical debt)
- `gofmt` only (no goimports/gofumpt)
- Cost estimators registered by resource `TypeName`, not by cloud provider
- Every mutating operation estimates cost before execution (`setup --dry-run`)

## Code Style Gotchas

- Acronyms: `ID`, `ARN`, `URL`, `AWS`, `IAM` (fully uppercase in names)
- AWS type aliases when needed: `gltypes`, `cftypes` (only for naming conflicts)
- Files: `snake_case.go`; build-tagged: `checker_windows.go`

## Files That Matter

| File | Purpose |
|------|---------|
| `PHASE_0_PLAN.md` | Authoritative implementation plan, file-by-file spec, dependency graph |
| `Fabrica_PRODUCT_SPEC.md` | Full product spec, vision, V1 scope |
| `CLAUDE.md` | Build commands (once code exists), architecture notes |
| L Ludus | `F:/source/ludus/` — reference for every Go CLI convention |

## Reference Repos

- **Ludus** (`F:/source/ludus/`) — flat `main.go`, `cmd/<name>/` subpackages, `internal/<domain>/`, Viper in `config.Load()`, diagnostic free-functions, stdlib-only tests
- **Classis** — provider registry pattern (`Backend` interface + `init()` registration)

## Lint & Test Commands (Once Code Exists)

```bash
golangci-lint run ./...
go build ./...
go vet ./...
go test ./... -race -cover
```

Coverage target: 60%+ for `internal/*` packages. Unit tests must use mocked SDK interfaces — no real AWS calls.

## Layering Check

After any change to `internal/`, verify:
```bash
go list -deps ./internal/cloud/...
```
Must NOT contain `internal/state`, `internal/cost`, or `cmd/*`.
