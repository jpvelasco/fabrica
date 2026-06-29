# Fabrica Roadmap

This is the single source of truth for where Fabrica is and where it's going.
The `README.md` describes how to use what exists today; this document tracks
status and sequencing. When they disagree, this file wins.

Last updated: 2026-06-28.

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
| **Fabrica** (this project) | Studio infrastructure provisioner. Stands up Perforce, Horde, CI, deploy targets, cost dashboards, and the shared state backend. |
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
See [`PHASE_0_PLAN.md`](PHASE_0_PLAN.md) for the detailed record.

### Phase 1 — Production-ready core 🚧 In progress

Turn the skeleton into a cohesive, production-grade tool. The three provisioning
modules and real Cloud Control CRUD are done; the work below is sequenced into
ordered milestones.

**Foundation already landed:**

- ✅ Perforce module (`create`/`status`/`destroy`)
- ✅ Horde module (`create`/`status`/`submit`/`destroy`/`ami build`)
- ✅ Workstation module (`create`/`list`/`stop`/`start`/`terminate`)
- ✅ Cloud Control CRUD against the real AWS API (all five `ResourceClient` methods)

**Milestone 1 — Foundation & first-run experience** *(highest priority)*

- ✅ Real **`fabrica setup`** — S3 (versioning + encryption + public-access-block) + DynamoDB bootstrap via `StateBackendBootstrapper`, idempotent, with cost preview, y/N confirmation (`--yes` skips), and dry-run
- ✅ Aggregate **`fabrica status`** — single read-only command showing backend health + per-module status, resource counts, and next steps; `--probe` opt-in TCP readiness checks
- ⬜ Polish first-run experience and error messaging

**Milestone 2 — CI module**

- ✅ `fabrica ci setup`/`trigger`/`status`/`logs` — CodeBuild orchestration layer over Horde (IAM role via Cloud Control, CodeBuild project via SDK runner)
- ✅ Integration with Horde (trigger resolves coordinator address, submits BuildGraph job) + Perforce (IAM read access; active sync deferred)

**Milestone 3 — Deploy module**

- ✅ `fabrica deploy setup`/`promote`/`rollback`/`status`/`destroy` — GameLift blue/green deployment orchestration, alias-flip promotion, instant rollback to retained fleets

**Milestone 4 — Cost management**

- ⬜ `fabrica cost report`/`forecast`/`alerts`
- ⬜ Multi-module reporting and budget guardrails

**Milestone 5 — Polish & release readiness**

- ⬜ End-to-end testing + teardown
- ⬜ Comprehensive documentation and examples
- ⬜ Final architecture + consistency review
- ⬜ v0.1 / v1.0 release preparation

**Also tracked under Phase 1:** Perforce `backup`/`restore`.

### Phase 2+ — Expansion 🔭 Future

- Lore support (production server management)
- Advanced DDC (distributed Zen / ScyllaDB)
- MCP server wrapping the same business-logic functions
- Multi-cloud / provider extensibility (GCP/Azure against the existing `cloud.Provider` interface)
- Export capabilities — `fabrica export --format cloudformation|terraform`
- Monitoring, alerts, and operational tools
- Drift detection + `--fix` auto-remediation
- Vigiles integration: telemetry + cost-data feed
- Multi-region state, state encryption key rotation

## Module status

| Module | Commands | Status |
|--------|----------|--------|
| Foundation | `doctor`, `config show`, `version` | ✅ Complete |
| `setup` | `setup` (`--dry-run`, `--yes`) | ✅ Complete — creates S3 bucket + DynamoDB table (idempotent, confirmed) |
| `perforce` | `create`, `status`, `destroy` | ✅ Complete (`backup`/`restore` planned) |
| `horde` | `create`, `status`, `submit`, `destroy`, `ami build` | ✅ Complete |
| `workstation` | `create`, `list`, `stop`, `start`, `terminate` | ✅ Complete |
| `status` (aggregate) | `status` (`--probe`, `--json`) | ✅ Complete — read-only health overview across all modules |
| `ci` | `setup`, `trigger`, `status`, `logs` | ✅ Complete — CodeBuild orchestration over Horde |
| `deploy` | `setup`, `promote`, `rollback`, `status`, `destroy` | ✅ Complete — GameLift blue/green deploy orchestration |
| `cost` | `report`, `forecast`, `alerts` | ⬜ Planned (Phase 1, Milestone 4) |
| `destroy --all` | clean teardown | ⚠️ Skeleton wired |
| `export` | `--format cloudformation\|terraform` | ⬜ Planned (Phase 2+) |

## Architecture decisions (locked)

- **IaC:** AWS Cloud Control API — no Terraform, Pulumi, or external binaries
- **Module path:** `github.com/jpvelasco/fabrica`
- **Go version:** 1.25.11
- **Config:** Viper + YAML, scoped inside `internal/config` only; `fmt.Print*` for output, no logging library
- **State:** S3 bucket (`fabrica-state-<account-id>`) + DynamoDB lock table (`fabrica-state-lock`); local `.fabrica/state.json` cache
- **Cost:** estimators registered by resource `TypeName`, provider-agnostic

See [`CLAUDE.md`](CLAUDE.md) for the contributor-facing architecture detail and
module-authoring guide.
