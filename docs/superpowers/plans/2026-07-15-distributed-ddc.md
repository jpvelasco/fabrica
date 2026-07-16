# Distributed DDC — Implementation Plan (Phase 2, Milestone 2)

> Companion to the **Approved** design:  
> [`docs/superpowers/specs/2026-07-15-distributed-ddc-design.md`](../specs/2026-07-15-distributed-ddc-design.md).

**Goal:** Ship `fabrica ddc {setup,status,destroy}` — single home-region, one co-located EC2, default `zen` + optional 1-node Scylla, hybrid EBS+S3, topology types for later expansion only, cost + destroy-all. Same risk profile as Lore V1.

**Hard ceiling:** No `region add`, no multi-region runtime, no replication peers, no client mounts, no EKS/OIDC.

> **No `region add` (or any multi-region command) in V1** — deferred to a later milestone.

> **Scylla backend in V1 is a single-node bootstrap path only.** It is **not** production HA.
> Use default `zen` unless you explicitly need Scylla and accept the limitations.

**destroy-all order (locked):** `deploy → ci → workstation → ddc → horde → lore → perforce`

---

## Global constraints

- Go 1.25.12+; `gofmt`; `golangci-lint` clean; Conventional Commits; feature branch off `main`.
- `internal/cloud/*` must not import `internal/state`, `internal/cost`, or `cmd/*`.
- `internal/ddc` and `internal/topology` are **SDK-free**.
- Two-file tests per command package; Codecov patch ≥90%; **no new function at 0%**.
- Seam injection on `command` structs.
- Do **not** re-register `AWS::EC2::Instance` or `AWS::EC2::Volume`.
- Do **not** add multi-region provider APIs for this milestone.

---

## Reuse list

| Existing piece | Use |
|----------------|-----|
| `cmd/lore` create/status/destroy | EC2 + SG + modstatus/teardown shape |
| `cmd/horde` create | AMI-first, CIDR `0.0.0.0/0` warning style |
| `cmd/ci/setup` | Multi-resource setup: dry-run, cost, confirm, ordered create |
| `cmd/internal/teardown` | Destroy spine; ResourceOrder if needed |
| `cmd/internal/modstatus` | Status flow + DDC Renderer |
| `cmd/internal/provision` | ReadState / confirm helpers |
| `cmd/internal/destroyall` | Register ddc teardown |
| `cmd/internal/costsource` | `"ddc"` case |
| `internal/perforce/cost.go` | Shared EC2/EBS prices |
| `internal/credentials` | Write `.fabrica/ddc-endpoints.yaml` (0600) |
| `internal/stateutil` | Lookup by type |
| `cloud.ResourceClient` | Cloud Control CRUD |
| VPCResolver pattern | In `internal/ddc` plan |
| `test/e2e` fake provider | setup → status → destroy |
| Doc-drift guard | README documents ddc commands |

---

## Critical files

**Create**

- `internal/topology/topology.go` (+ tests)
- `internal/ddc/{plan,resources,userdata,backend,cost,endpoints}.go` (+ tests)
- `cmd/ddc/ddc.go`
- `cmd/ddc/setup/` · `status/` · `destroy/` (each: impl + white-box + cobra tests)
- `docs/ddc-ami.md`
- `test/e2e/ddc_test.go`

**Touch**

- `internal/config/config.go` — `DDCConfig`
- `cmd/root/root.go`
- `cmd/destroy/destroy.go` — order + teardown
- `cmd/internal/costsource/costsource.go`
- Aggregate status if it hardcodes modules
- `fabrica.example.yaml`, `README.md`, `ROADMAP.md`, `CLAUDE.md`, `AGENTS.md`, `CHANGELOG.md`

**Do not create**

- `cmd/ddc/region/*`
- Replication / peer / multi-region helpers as product features

---

## Numbered tasks

### Task 1 — `internal/topology`

**Files:** `internal/topology/topology.go`, `topology_test.go`

1. `Role` (`Coordinator`, `Edge`), `NodeSpec`, `Topology` (`HomeRegion`, `Coordinator`, `Edges`).
2. `NewHomeCoLocated(region, node NodeSpec) Topology` — records **both roles** for one logical host (coordinator required; home edge co-located representation per design).
3. `Validate()` — home region set; coordinator set; **reject** any `Edges` entry whose `Region != HomeRegion` (guards accidental multi-region graphs in V1 callers).
4. No AWS types, no ports, no peer fields.

**Verify:** `go test ./internal/topology/ -count=1` — exported coverage complete.

---

### Task 2 — `DDCConfig`

**Files:** `internal/config/config.go`, tests

```go
type DDCConfig struct {
    Backend            string `mapstructure:"backend" yaml:"backend"` // zen|scylla; default zen in plan
    AmiID              string `mapstructure:"amiId" yaml:"amiId"`
    ScyllaAmiID        string `mapstructure:"scyllaAmiId" yaml:"scyllaAmiId"`
    InstanceType       string `mapstructure:"instanceType" yaml:"instanceType"`
    VolumeSize         int    `mapstructure:"volumeSize" yaml:"volumeSize"`
    ScyllaInstanceType string `mapstructure:"scyllaInstanceType" yaml:"scyllaInstanceType"`
    ScyllaVolumeSize   int    `mapstructure:"scyllaVolumeSize" yaml:"scyllaVolumeSize"`
    VPCId              string `mapstructure:"vpcId" yaml:"vpcId"`
    SubnetId           string `mapstructure:"subnetId" yaml:"subnetId"`
    AllowedCIDR        string `mapstructure:"allowedCidr" yaml:"allowedCidr"`
    InternalCIDR       string `mapstructure:"internalCidr" yaml:"internalCidr"`
    PublicPort         int    `mapstructure:"publicPort" yaml:"publicPort"`
    InternalPort       int    `mapstructure:"internalPort" yaml:"internalPort"`
    Bucket             string `mapstructure:"bucket" yaml:"bucket"`
    Namespace          string `mapstructure:"namespace" yaml:"namespace"`
}
```

Defaults applied in plan layer (not Viper soup): backend `zen`, instance `m7i.xlarge`, volume 500, CIDRs `10.0.0.0/8`, ports 80/8080, namespace `deriveddatacache`.

**Verify:** load sample YAML; empty backend → treated as zen in plan tests.

---

### Task 3 — `internal/ddc` plan layer (TDD)

**Files:** plan, resources, backend, userdata, cost, endpoints + tests

1. **`SetupPlan`** — account, home region only, topology from `NewHomeCoLocated`, backend kind, bucket, ports, CIDRs, cost resources.
2. **`NewSetupPlan(cfg, account, region, resolver)`**
   - Require `amiId`
   - Default backend `zen`
   - If backend `scylla`, require `scyllaAmiId` with error that says this is advanced 1-node bootstrap, not HA
   - Bucket default `fabrica-ddc-<account>-<region>`
   - Single-region VPC resolve only
3. **Desired-state:** IAM, S3 bucket, SG (public + internal; 9042 only for scylla), one DDC instance, optional one Scylla instance
4. **`Generate` / `GenerateRaw`** — Jupiter single-host config (S3, namespace, ports; Scylla contacts only if scylla). **No peer region list, no replication block for remote edges.**
5. **`CostResources`** — EC2+EBS (+ Scylla) + S3
6. **Endpoints** — single public (and optional internal) URL; YAML for endpoints file
7. **Warning helpers** (pure strings for cmd to print):
   - `WarnOpenCIDR(cidr)` when `0.0.0.0/0` (Horde-style wording)
   - `WarnScyllaBootstrap()` when backend is scylla (1-node, not production HA, prefer zen)

**Verify:** `go test ./internal/ddc/ -count=1` — goldens for JSON/UserData; tests assert no multi-region fields in generated config.

---

### Task 4 — `cmd/ddc setup`

**Files:** `cmd/ddc/ddc.go`, `cmd/ddc/setup/*`

1. State has `ddc` → exit clean (“already provisioned”).
2. Plan → dry-run (plan + cost + CIDR/Scylla warnings) → return if dry-run.
3. Confirm y/N (`--yes` skips).
4. Create order: IAM → Bucket → SG → [Scylla] → DDC instance.
5. State after each resource; Properties: `region` (home), `role`, `instanceType`, `volumeSize`.
6. Write `.fabrica/ddc-endpoints.yaml`; Next steps: health curl, `UE-CloudDataCacheHost=…`.
7. Flag `--backend` overrides config for this run if you support it (optional; config alone is enough — if flag added, keep it thin).

Seams: `readState`, `writeState`, `createResource`, `confirm`, `writeEndpoints`.

**Verify:** dry-run warnings; confirm reject; partial failure; idempotent re-run; scylla path creates extra instance; zen does not.

---

### Task 5 — `cmd/ddc status`

**Files:** `cmd/ddc/status/*`

1. modstatus engine + DDC Renderer (or lore/status pattern).
2. Get DDC instance; optional Scylla row for display only.
3. `--probe` → `/health/ready` on DDC public port; `provisioning` → `ready`.
4. Table + `--json` (status, single endpoint set, backend, resource ids). **No regions array of remotes.**
5. Soft Horde next-step line if horde in state.

**Verify:** missing module; probe ok/fail; json shape.

---

### Task 6 — destroy + destroy-all + costsource

**Files:** `cmd/ddc/destroy/*`, `cmd/destroy/destroy.go`, `cmd/internal/costsource/*`

1. Delete order: DDC instance → Scylla (if any) → Bucket (refuse non-empty) → IAM → SG.
2. ConfirmExact (or project-standard destructive confirm) like perforce destroy.
3. `RunOrchestrated` for destroy-all.
4. Order: `deploy → ci → workstation → ddc → horde → lore → perforce`
5. `costsource` `"ddc"` case (state Properties first).

**Verify:** full destroy; non-empty bucket; destroy-all includes ddc; cost report line.

---

### Task 7 — Docs, wiring, E2E

1. `docs/ddc-ami.md` — AMI contents, ports, health, S3 IAM; **zen default**; scylla optional 1-node warning.
2. `fabrica.example.yaml` — commented `ddc:` (backend zen default called out).
3. README `### DDC` — three commands only.
4. ROADMAP — Phase 2 Milestone 2 DDC V1 approved/implemented status when done; multi-region explicitly later.
5. CLAUDE.md / AGENTS.md — commands, destroy order, no region add.
6. CHANGELOG `[Unreleased]`.
7. `cmd/root` register parent with **only** setup/status/destroy.
8. `test/e2e/ddc_test.go` — setup → status → destroy.

**Verify:**

```bash
go test ./...
go test ./test/e2e/ -count=1 -run DDC
golangci-lint run ./...
go list -deps ./internal/cloud/...
```

Grep guard (manual or test): no `region add` command registration; no `ForRegion` usage in `cmd/ddc` / `internal/ddc`.

---

### Task 8 — Coverage + PR

1. Patch ≥90%; no 0% funcs.
2. PR body: Approved design link; V1 ceiling (no multi-region); scylla warnings; CIDR warnings.
3. CI green (private-repo workaround if needed).

---

## Suggested commits

1. `feat(topology): add home co-located coordinator/edge types`
2. `feat(config): add DDCConfig`
3. `feat(ddc): plan layer for single-region setup`
4. `feat(ddc): setup command`
5. `feat(ddc): status command`
6. `feat(ddc): destroy, destroy-all, and costsource`
7. `docs(ddc): ami guide, README, ROADMAP, example yaml`
8. `test(e2e): ddc setup status destroy flow`

---

## Verification checklist

| # | Check |
|---|--------|
| 1 | `ddc setup --dry-run` — one DDC host (+ optional Scylla), cost, open-CIDR warning if needed |
| 2 | Scylla backend prints bootstrap/non-HA warning; zen is default |
| 3 | Setup idempotent; state after each resource; role/region Properties |
| 4 | `ddc status --probe` → `/health/ready` |
| 5 | Only setup/status/destroy registered — **no region add** |
| 6 | UserData/plan contain **no** remote peer / multi-region replication config |
| 7 | Destroy reverse-order; non-empty S3 refused |
| 8 | `destroy --all` order includes ddc before horde |
| 9 | `cost report` includes ddc |
| 10 | Topology used by plan; `Validate` rejects foreign-region edges |
| 11 | E2E green; layering clean |

---

## Risk notes

| Risk | Mitigation |
|------|------------|
| Jupiter config drift | Pin in `docs/ddc-ami.md`; `missingkey=error` templates |
| Users pick scylla by accident | Default zen; hard require scyllaAmiId; loud warnings |
| Scylla 1-node data loss | Docs + setup stderr warning; not marketed as HA |
| `0.0.0.0/0` without auth | Strong warning like Horde |
| Scope creep (region add, peers) | Design Out of Scope + this ceiling; reject in review |
| Cloud Control gaps (S3/IAM) | Early create test; SDK auxiliary only if CREATE unsupported (ci pattern) |
| UE/AMI licensing | Document in ami guide |

---

## Effort sketch

| Block | Rough |
|-------|--------|
| Topology + config + plan | 1.5–2 d |
| setup + status | 1.5–2 d |
| destroy + cost + destroy-all | 1 d |
| Docs + E2E + coverage | 1 d |
| **Total** | **~5–6 engineer-days** |

---

## Done means

1. Single-region Unreal Cloud DDC provisioned (`zen` default, optional scylla bootstrap).
2. Status + probe against one host.
3. Destroy and destroy-all work; cost report includes ddc.
4. Topology package exists for **future** multi-region — **no multi-region behavior in V1.**

**Not done if:** region commands, peer replication, client mounts, or EKS/OIDC are required to claim complete.
