# Fabrica Roadmap

This is the single source of truth for where Fabrica is and where it's going.
The `README.md` describes how to use what exists today; this document tracks
status and sequencing. When they disagree, this file wins.

Last updated: 2026-08-07.

## Vision

Fabrica is the studio infrastructure command center. It provisions and manages
production-grade AWS resources so game studios can focus on making games instead
of wrestling with cloud infrastructure — provision, check status, and tear down
the full stack (source control, build farms, CI/CD, deploy targets, cost
visibility) from a single YAML config, with cost estimates before anything
touches the account and DynamoDB-backed state so engineers don't clobber each
other's runs.

## The Praetorium constellation

Fabrica is one tool in a larger family of game-infrastructure tooling —
internally **Praetorium** until the full set ships. Each tool is cohesive on
its own and composes with the others without tight coupling:

| Tool | Role |
|------|------|
| **Ludus** | Unreal Engine 5 developer workstation tool. First to ship; source of every Go CLI convention Fabrica follows. |
| **Fabrica** (this project) | Studio infrastructure provisioner. Stands up Perforce/Lore, Horde, DDC, CI, deploy targets, cost visibility, and the shared state backend. |
| **Classis** | Cloud-agnostic fleet control tower for game servers (GameLift today; Agones/raw EC2/GCE next). |
| **Nuntius** | Dedicated GameLift MCP server. Lets Claude drive fleet operations directly. |
| **Vigiles** *(future)* | Shared intelligence layer: anomaly detection, cost forecasting, diagnostics, predictive scaling. Consumes telemetry from Fabrica and Classis. |
| **Praetorium** | Umbrella name for the whole empire. Revealed once the constellation is complete. |

**How Fabrica fits:** Fabrica owns the *studio-level infrastructure layer*. It
provisions the foundational systems (source control, build farms, CI/CD, shared
state) the rest of the empire depends on. Ludus consumes BuildGraph output from
Fabrica's Horde; Classis will consume deployment targets and state; Vigiles will
consume telemetry and cost data. The `cloud.Provider` interface is the same
abstraction Classis uses for its backends — this is how the constellation stays
cohesive while loosely coupled to any one cloud.

## Design principles

These govern every structural decision and carry across all phases.

1. **High cohesion, loose coupling.** Each `internal/<domain>` package owns one concern behind a narrow interface. No package imports a sibling's internals.
2. **CLI-first, MCP-native.** Every capability ships as a Cobra command first; MCP tools (later) wrap the same business-logic functions. Command logic lives in `internal/*`, not `cmd/*`.
3. **Day-2 is first-class.** `doctor`, `status`, drift detection, and cost reporting are not afterthoughts.
4. **Clear resource ownership + layered architecture.** Strict one-way dependency flow: `cmd/* → internal/<domain> → internal/cloud`. No domain package imports `cmd/*`; no `internal/cloud` impl imports a sibling domain.
5. **Cost transparency.** Every mutating operation estimates monthly cost before execution. `--dry-run` prints the bill.
6. **Reconciliation mindset.** Operations are idempotent. State on S3 is canonical; local `.fabrica/state.json` is a cache.

**UI strategy:** CLI-first + MCP-native. No web or desktop UI is planned. Any
future unified console (the "Praetorium Console") would be a separate product.

## Phases

### Phase 0 — Walking skeleton ✅ Complete

CLI skeleton, config, `cloud.Provider` interface + AWS implementation, state
schema, `doctor`, `version`, `config show`, cost-estimator registry, CI, lint.
Established the architecture every later module drops into without refactor.

### Phase 1 — Production-ready core ✅ Complete

> 🎉 **Phase 1 (Foundation + Core Pipeline) completed** — Perforce, Horde,
> Workstation, CI, Deploy, Cost, Setup, Status, full teardown, E2E harness, and
> release machinery are all production-ready.

Turned the skeleton into a cohesive, production-grade tool: six provisioning/
management modules, real Cloud Control CRUD, full-stack teardown, offline cost
visibility, a CLI E2E harness, and release machinery (GoReleaser + npm). All five
milestones below are done. Remaining nice-to-haves (residual test-coverage
gaps) are tracked at the end and do not block Phase 1.

**Foundation already landed:**

- ✅ Perforce module (`create`/`status`/`destroy`)
- ✅ Horde module (`create`/`status`/`submit`/`destroy`/`ami build`)
- ✅ Workstation module (`create`/`list`/`stop`/`start`/`terminate`)
- ✅ Cloud Control CRUD against the real AWS API (all five `ResourceClient` methods)

**Milestone 1 — Foundation & first-run experience** *(highest priority)*

- ✅ Real **`fabrica setup`** — S3 (versioning + encryption + public-access-block) + DynamoDB bootstrap via `StateBackendBootstrapper`, idempotent, with cost preview, y/N confirmation (`--yes` skips), and dry-run
- ✅ Aggregate **`fabrica status`** — single read-only command showing backend health + per-module status, resource counts, and next steps; `--probe` opt-in TCP readiness checks
- ✅ Polish first-run experience and error messaging — actionable errors, consistent [OK]/[WARN]/[FAIL] indicators + aligned tables + "Next steps" guidance across status/cost/ci/deploy

**Milestone 2 — CI module**

- ✅ `fabrica ci setup`/`trigger`/`status`/`logs` — CodeBuild orchestration layer over Horde (IAM role via Cloud Control, CodeBuild project via SDK runner)
- ✅ Integration with Horde (trigger resolves coordinator address, submits BuildGraph job) + Perforce (IAM read access; active sync deferred)

**Milestone 3 — Deploy module**

- ✅ `fabrica deploy setup`/`promote`/`rollback`/`status`/`destroy` — GameLift blue/green deployment orchestration, alias-flip promotion, instant rollback to retained fleets

**Milestone 4 — Cost management**

- ✅ `fabrica cost report`/`forecast`/`alerts`
- ✅ Multi-module reporting and budget guardrails
- ✅ Backfill ModuleResource.Properties with cost inputs at create time — perforce/horde/workstation record instanceType+volumeSize, deploy records instanceType+desiredInstances; `costsource` reads state-first with config fallback (#71)

**Milestone 5 — Polish & release readiness**

- ✅ End-to-end full-stack teardown (`destroy --all`)
- ✅ End-to-end testing (CLI E2E harness — in-process, fake provider, runs in CI)
- ✅ README refresh (full command coverage) + doc-drift CI guard
- ✅ Final architecture + consistency review (clean layering; doc/cleanup fixes applied; test-coverage gaps tracked as a follow-up)
- ✅ Release machinery — GoReleaser + npm shim (ludus-cli pattern); first public cut is **v0.1.0**

**Post-v0.1.1 quality sprint** (complete as of 2026-08-01):
- Structural deduplication across plan, cost, userdata, IAM, status, teardown, and test layers — shared packages (`internal/ec2plan`, `internal/ec2state`, `internal/ec2cost`, `internal/userdata`, `internal/iamrole`, `cmd/internal/testutil`) and expanded `cmd/internal/provision` / `modstatus` / `teardown`
- Test coverage raised across the board (most packages mid–high 90%s); previous white-box gaps (horde destroy, workstation terminate) closed
- Codacy profile locked (cloud + local mirror); CI/gosec split documented
- Residual cosmetic conventions (output-writer seam in `cmd/version`, multi-letter anonymous receivers) remain low-priority

### Phase 2 / v0.2 — Lore module ✅ Implemented

- ✅ Design approved: single-region AMI-first `loreserver` (EC2 + SG + EBS), `create`/`status`/`destroy`
- ✅ Implementation: parallel to Perforce; local/EBS store; no multi-region in V1

### Phase 2 / Milestone 2 — Distributed DDC ✅ Implemented

- ✅ Design approved: single home-region EC2 (co-located coordinator + edge roles)
- ✅ Commands: `ddc setup` / `status` / `destroy`; home-region V1
- ✅ Zen (Jupiter) default + optional 1-node Scylla bootstrap; hybrid EBS + S3
- ✅ Multi-region edge nodes: `ddc region add` provisions peer-region edge SG + EC2 sharing the home blob bucket and IAM profile; `status` lists edges from state; `destroy` tears down edges (per region) then the home stack
- ✅ `internal/topology` coordinator/edge graph types; `cloud.RegionProvider` region-scoped clients
- Deferred: replication peers (operator-managed today), OIDC/HTTPS, `ddc ami build`, Scylla HA

### Phase 2+ — Expansion 🔭 Future

- Perforce backup follow-ups: scheduled backups, DR rehydrate from S3/orphan volume, attach-role migration for pre-SSM stacks
- Lore follow-ups: S3-backed store, `lore ami build`, JWT/CA TLS, client helpers
- DDC: OIDC, production Scylla, replication-peer automation, live edge probes in `ddc status`
- ✅ MCP server V1: 6 read-only tools (`fabrica_version`, `fabrica_doctor`, `fabrica_status`, `fabrica_drift`, `fabrica_cost_report`, `fabrica_config_show`) over stdio transport — reuses same business logic as CLI
- MCP server V2: destructive tools, streaming, resource management
- Multi-cloud / provider extensibility (GCP/Azure against the existing `cloud.Provider` interface)
- ✅ Export capabilities — `fabrica export --format cloudformation|terraform` (V1: state backend + Horde/Perforce/Lore; DDC/Workstation/CI/Deploy deferred to V2)
- Monitoring, alerts, and operational tools
- Drift `--fix` auto-remediation (read-only drift detection is implemented)
- Vigiles integration: telemetry + cost-data feed
- Multi-region state, state encryption key rotation

## Module status

| Module | Commands | Status |
|--------|----------|--------|
| Foundation | `doctor`, `config show`, `version` | ✅ Complete |
| `setup` | `setup` (`--dry-run`, `--yes`) | ✅ Complete — creates S3 bucket + DynamoDB table (idempotent, confirmed) |
| `perforce` | `create`, `status`, `destroy`, `backup`, `backup list`, `backup delete`, `restore` | ✅ Complete — EBS backup/restore via SSM; optional S3 export |
| `horde` | `create`, `status`, `submit`, `destroy`, `ami build` | ✅ Complete |
| `lore` | `create`, `status`, `destroy` | ✅ Complete (v0.2) — AMI-first loreserver; parallel to Perforce |
| `ddc` | `setup`, `status`, `destroy`, `region add` | ✅ Complete — home-region Unreal Cloud DDC + additional edge regions; no replication-peer automation (operator-managed) |
| `workstation` | `create`, `list`, `stop`, `start`, `terminate` | ✅ Complete |
| `status` (aggregate) | `status` (`--probe`, `--json`) | ✅ Complete — read-only health overview across all modules |
| `drift` | `drift` (`--json`) | ✅ Complete — read-only drift detection: state backend, EC2 instances (state, type, AMI), SGs, IAM roles, CodeBuild projects, Extra resource detection (live-not-in-state via ResourceList) |
| `ci` | `setup`, `trigger`, `status`, `logs`, `destroy` | ✅ Complete — CodeBuild orchestration over Horde; `destroy` removes CodeBuild project + IAM role |
| `deploy` | `setup`, `promote`, `rollback`, `status`, `destroy` | ✅ Complete — GameLift blue/green deploy orchestration |
| `cost` | `report`, `forecast`, `alerts` | ✅ Complete — offline config-derived report/forecast + local budget alerts |
| `destroy --all` | clean teardown | ✅ Complete — tears down all modules (deploy→ci→workstation→ddc→horde→lore→perforce) then the state backend; backend deleted only on full success |
| `export` | `--format cloudformation\|terraform` | ✅ Complete (V1) — CloudFormation YAML and Terraform HCL from local state; state backend + Horde/Perforce/Lore; secrets redacted |

## Architecture decisions (locked)

- **IaC:** AWS Cloud Control API — no Terraform, Pulumi, or external binaries
- **Module path:** `github.com/jpvelasco/fabrica`
- **Go version:** 1.25.12
- **Config:** Viper + YAML, scoped inside `internal/config` only; `fmt.Print*` for output, no logging library
- **State:** S3 bucket (`fabrica-state-<account-id>`) + DynamoDB lock table (`fabrica-state-lock`); local `.fabrica/state.json` cache
- **Cost:** estimators registered by resource `TypeName`, provider-agnostic

See [`CLAUDE.md`](CLAUDE.md) for the contributor-facing architecture detail and
module-authoring guide.
