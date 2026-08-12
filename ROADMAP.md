# Fabrica Roadmap

This document tracks what Fabrica ships today and where it's headed next.
The `README.md` describes how to use what exists; this file tracks status and
sequencing. When they disagree, this file wins.

Last updated: 2026-08-10.

## What Fabrica Is

A single-binary Go CLI that provisions and manages game studio cloud
infrastructure on AWS — Perforce/Lore version control, Unreal Horde build
farms, Distributed DDC, CI/CD, GameLift deployment, and cloud workstations.
One YAML config, cost estimates before any write, DynamoDB-locked state so
two engineers don't clobber each other's runs.

Sister tool to [Ludus](https://github.com/jpvelasco/ludus): Ludus orchestrates
game builds; Fabrica gives them somewhere to run.

## Design Principles

These govern every structural decision and carry across all phases.

1. **CLI-first.** Every capability ships as a Cobra command first. MCP tools wrap the same business-logic functions.
2. **Cost before write.** Every mutating operation estimates monthly cost before execution. `--dry-run` prints the bill.
3. **Idempotent operations.** State on S3 is canonical; local `.fabrica/state.json` is a cache. Re-running detects already-provisioned resources and exits cleanly.
4. **Day-2 is first-class.** `doctor`, `status`, drift detection, and cost reporting are not afterthoughts.
5. **Clear resource ownership.** Strict one-way dependency flow: `cmd/* → internal/<domain> → internal/cloud`.
6. **Reconciliation mindset.** Operations are idempotent. Partial failures leave recoverable state.

## Current Status

**Current stable: v0.3.5** (2026-08-10). Phase 0, Phase 1, Lore (v0.2), and
DDC (V1 + multi-region edge nodes with live edge probes) are all complete. Export V2 covers all 8
modules (state backend, Horde, Perforce, Lore, DDC, Workstation, CI, Deploy).
Ops logging (`--verbose` / `FABRICA_LOG_LEVEL`) ships in this release.

| Module | Commands | Status |
|--------|----------|--------|
| Foundation | `doctor`, `config show`, `version` | ✅ Complete |
| `setup` | `setup` (`--dry-run`, `--yes`) | ✅ Complete — creates S3 bucket + DynamoDB table (idempotent, confirmed) |
| `perforce` | `create`, `status`, `destroy`, `backup`, `backup list`, `backup delete`, `restore` | ✅ Complete — EBS backup/restore via SSM; optional S3 export |
| `horde` | `create`, `status`, `submit`, `destroy`, `ami build` | ✅ Complete |
| `horde agents` | `create`, `status`, `destroy` | ✅ Complete (V1) — managed agent pool (ASG + Launch Template); private subnets, SSM-only access, coordinator enrollment via private IP; manual min/desired/max capacity |
| `lore` | `create`, `status`, `destroy` | ✅ Complete (v0.2) — AMI-first loreserver; parallel to Perforce |
| `ddc` | `setup`, `status`, `destroy`, `region add` | ✅ Complete — home-region Unreal Cloud DDC + additional edge regions; no replication-peer automation (operator-managed) |
| `workstation` | `create`, `list`, `stop`, `start`, `terminate` | ✅ Complete |
| `status` (aggregate) | `status` (`--probe`, `--json`) | ✅ Complete — read-only health overview across all modules |
| `drift` | `drift` (`--json`, `--fix`) | ✅ Complete — drift detection + auto-remediation: state backend, EC2 instances (state, type, AMI), SGs, IAM roles, CodeBuild projects, Extra resource detection. `--fix` recreates Missing resources from recorded state; Mismatch/Extra report-only |
| `ci` | `setup`, `trigger`, `status`, `logs`, `destroy` | ✅ Complete — CodeBuild orchestration over Horde; `destroy` removes CodeBuild project + IAM role |
| `deploy` | `setup`, `promote`, `rollback`, `status`, `destroy` | ✅ Complete — GameLift blue/green deploy orchestration |
| `cost` | `report`, `forecast`, `alerts` | ✅ Complete — offline config-derived report/forecast + local budget alerts |
| `destroy --all` | clean teardown | ✅ Complete — tears down all modules (deploy→ci→workstation→ddc→horde→lore→perforce) then the state backend; backend deleted only on full success |
| `export` | `--format cloudformation\|terraform` | ✅ Complete (V2) — CloudFormation YAML and Terraform HCL from local state; all modules (state backend, Horde, Perforce, Lore, DDC, Workstation, CI, Deploy); secrets redacted |
| `mcp` | `mcp` | ✅ Complete — stdio MCP server (6 read-only tools) |
| Ops logging | `--verbose`, `FABRICA_LOG_LEVEL` | ✅ Complete (V1) — stdlib `log/slog` via `internal/oplog`; stderr diagnostics for state I/O, Cloud Control errors, drift --fix, destroy milestones, bootstrap failures; secrets never logged |

## Possible Future Work

- Deeper day-2 operations: scheduled Perforce backups, DR rehydrate, attach-role migration for pre-SSM stacks
- Lore follow-ups: S3-backed store, `lore ami build`, JWT/CA TLS
- DDC: OIDC, production Scylla, replication-peer automation
- MCP server V2: destructive tools, streaming, resource management
- Optional observability: monitoring, alerts, operational dashboards
- Multi-cloud / provider extensibility (GCP/Azure against the existing `cloud.Provider` interface)
- Multi-region state, state encryption key rotation

## Architecture Decisions (Locked)

- **IaC:** AWS Cloud Control API — no Terraform, Pulumi, or external binaries
- **Module path:** `github.com/jpvelasco/fabrica`
- **Go version:** 1.25.12
- **Config:** Viper + YAML, scoped inside `internal/config` only
- **Output:** dual streams — human output via `fmt.Print*` to stdout; operational diagnostics via `internal/oplog` (stdlib `log/slog`) to stderr
- **State:** S3 bucket (`fabrica-state-<account-id>`) + DynamoDB lock table (`fabrica-state-lock`); local `.fabrica/state.json` cache
- **Cost:** estimators registered by resource `TypeName`, provider-agnostic

See [`CLAUDE.md`](CLAUDE.md) for the contributor-facing architecture detail and
module-authoring guide.