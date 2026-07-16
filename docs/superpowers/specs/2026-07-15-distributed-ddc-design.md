# Distributed DDC — Design Spec (Phase 2, Milestone 2)

**Date:** 2026-07-15  
**Status:** **Approved**  
**Scope:** V1 of `fabrica ddc` — **single home-region only**  
**Placement:** Phase 2 / Milestone 2 (after Lore v0.2)

---

## Goal

Provision a **studio-wide Derived Data Cache** so UE5 cooks and shader builds share content-addressed derived data instead of every developer and Horde agent rebuilding the same work locally.

V1 is intentionally **narrow and low-risk** (same posture as Perforce / Lore first cuts):

1. **One home-region** Unreal Cloud DDC (Jupiter / “Zen Cloud DDC”) on a **single EC2 instance**.
2. **Hybrid storage:** EBS hot tier + one S3 bucket for durable blobs (that home region only).
3. **Primary backend:** `zen` (default). **Optional advanced path:** `scylla` (1-node bootstrap — not HA).
4. **Server-only:** print endpoints + Next steps; no client mounts or ini patching.
5. **Cost preview** before any write; module **destroy** and **`destroy --all`** integration.
6. **`internal/topology` types** record coordinator + edge **roles** on that one instance so a *future* multi-region feature can extend the model without rewriting the module. **V1 does not implement multi-region.**

**Success:** `fabrica ddc setup` → healthy `/health/ready` → cook/Horde pointed at the printed host → `fabrica ddc destroy` (or `destroy --all`) cleans up. No Terraform, no EKS, no second region, no replication peers.

---

## What DDC is (brief)

| Term | Meaning |
|------|---------|
| **DDC** | UE Derived Data Cache (cooked/shader/etc. derived products) |
| **Unreal Cloud DDC / Jupiter** | Epic’s Cloud DDC service (HTTP APIs) |
| **Zen Cloud DDC** | Studio-facing name for the primary path (`backend: zen`) |
| **Scylla path** | Optional advanced metadata backend (`backend: scylla`) — V1 = single bootstrap node only |
| **Hybrid store** | Hot path on EBS; durable blobs on S3 |

Epic’s service exposes `/health/live` and `/health/ready` and expects blob storage (S3) plus a DB. Fabrica V1 wires a **single-region** deployment only.

**vs AWS Game Dev Toolkit:** Toolkit path is Scylla + EKS + S3 + heavy assembly. Fabrica uses **AMI-first EC2 + Cloud Control**, matching Perforce/Horde/Lore.

---

## Locked V1 decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Commands | `setup`, `status`, `destroy` **only** | No `region add` (or any multi-region command) in V1 |
| Home layout | **One EC2** in the config/cloud home region | Co-located coordinator + home edge **roles** on that instance |
| Topology code | `internal/topology` records both roles | Future split / extra edges without schema rewrite; **no extra instances in V1** |
| Primary backend | `zen` (default) | Correct path for almost all studios in V1 |
| Scylla | Optional, opt-in, advanced | 1-node bootstrap; strong warnings; not production HA |
| Storage | EBS + **one** S3 bucket in the home region | Hybrid single-region |
| Client surface | Server-only | Endpoints file + Next steps |
| Orchestration | Cloud Control EC2/SG/S3/IAM | No EKS/Helm |
| Cost | Estimators + `costsource` | Offline report like other modules |
| Teardown | Module destroy + destroy-all | Ordered with the stack |
| Multi-region runtime | **None** | No peer lists, no cross-region resources, no replication config in V1 code paths |

---

## Commands

```
fabrica ddc setup [--backend zen|scylla]
fabrica ddc status [--probe] [--json]
fabrica ddc destroy [--yes]
```

> **No `region add` (or any multi-region command) in V1** — deferred to a later milestone.
> V1 is single home-region only: no remote edges, no replication peers, no multi-region status.

### `ddc setup`

Provisions the **home-region only** stack (idempotent; dry-run + cost + y/N confirm):

1. IAM role + instance profile (S3 RW on the DDC bucket; optional SSM core for future day-2).
2. S3 bucket — versioning, encryption, public-access-block (default name `fabrica-ddc-<account>-<region>` or config override). One bucket for this deployment’s home region.
3. Security group — public API + internal API ports (internal left available for **future** use; V1 does not configure remote peers). Scylla CQL only if backend is `scylla`.
4. *(Only if `backend=scylla`)* One Scylla EC2 + data EBS. Print the Scylla bootstrap warning (below).
5. **One** DDC EC2 (AMI-first Jupiter) + hot EBS. Topology model tags this instance with both **coordinator** and **edge** roles (co-located); still a single physical host.

State written after each resource. Re-run with module present → clean exit.

**Backend default:** `zen`. Prefer omitting `--backend` / leaving `ddc.backend: zen`.

> **Scylla backend in V1 is a single-node bootstrap path only.** It is **not** production HA.
> Use default `zen` unless you explicitly need Scylla and accept the limitations
> (no RF=3, no multi-DC, data-loss/availability risk on that single node).

### `ddc status`

Read-only, **single deployment**:

- Module state + resource IDs
- Live EC2 via Cloud Control (DDC instance; Scylla instance if present)
- Optional `--probe`: HTTP `GET /health/ready` (and/or `/health/live`) on the public port of the DDC host
- `provisioning` → `ready` when probe succeeds (same pattern as perforce/horde/lore)
- Soft Horde hint if horde module exists (“point cooks at …”) — no mutation
- `--json` for automation

Does **not** list remote regions, edges, or replication peers (none exist in V1).

### `ddc destroy`

Reverse-order delete of **this module’s** resources only:

DDC instance → Scylla instance (if any) → S3 (refuse if non-empty; actionable message) → IAM → SG

- Skip already-gone resources (`ErrResourceNotFound`)
- `RunOrchestrated` entry for `destroy --all`
- **destroy-all order (locked):**  
  `deploy → ci → workstation → ddc → horde → lore → perforce`

Uses `cmd/internal/teardown` where the shape fits; multi-type resources (bucket, IAM, optional Scylla) may use a thin ddc-specific destroy (like `ci destroy`) on the same seams.

---

## Architecture

### Layers

```
cmd/ddc/{setup,status,destroy}
        ↓
internal/ddc/          # pure plan layer (no AWS SDK)
internal/topology/     # Role + NodeSpec + Topology (V1: one host, two roles recorded)
        ↓
internal/cloud (+ aws) # ResourceClient
```

Rules unchanged: `internal/cloud/*` never imports state/cost/cmd; `internal/ddc` may import `internal/topology`, not the reverse.

### Home layout (explicit)

| Aspect | V1 |
|--------|-----|
| Physical hosts (DDC service) | **Exactly one** EC2 instance |
| AWS region | Config / provider home region only |
| Coordinator role | Recorded on that instance |
| Home edge role | Recorded on the **same** instance (co-located) |
| Additional edge instances | **None** |
| Future | Topology types already distinguish roles so a later release can split coordinator vs edge onto separate instances or add remote edges **without** redesigning state vocabulary |

### Topology abstraction (`internal/topology`)

Present in V1 as **data model only** — not a multi-region control plane.

```go
type Role string // Coordinator | Edge

type NodeSpec struct {
    Role         Role
    Region       string
    InstanceType string
    AmiID        string
    VolumeSize   int
}

type Topology struct {
    HomeRegion  string
    Coordinator NodeSpec
    // V1: Edges is either empty or a single entry describing the co-located
    // home edge (same region/instance intent as Coordinator). No remote edges.
    Edges []NodeSpec
}
```

Helper such as `NewHomeCoLocated(region, node NodeSpec) Topology` builds the V1 graph: one logical host, both roles recorded for future splitting.

**V1 code must not:** create resources outside `HomeRegion`, iterate “all regions,” configure replication peer URLs, or expose CLI for adding edges.

### Backend

| Kind | Who it’s for | V1 behavior |
|------|----------------|-------------|
| **`zen` (default)** | **Almost all users** | Single Jupiter AMI instance; single-region metadata path via AMI + cloud-init. **Start here.** |
| **`scylla` (optional, advanced)** | Operators who explicitly need Scylla contact points | **One** Scylla EC2 + EBS plus the DDC instance. **Scylla backend in V1 is a single-node bootstrap path only. It is not production HA.** Use default `zen` unless you explicitly need Scylla and accept the limitations. |

If `backend=scylla` and `scyllaAmiId` is empty → hard error with what to set. Never silently fall back in a way that hides the advanced path.

### Storage (hybrid, single region)

| Tier | Store | V1 |
|------|-------|-----|
| Hot | EBS gp3 on the DDC instance | Required |
| Durable blobs | **One** S3 bucket in the home region | Required |
| Metadata | zen default path, or Scylla if opted in | Depends on backend |

No second-region buckets in V1. (A later multi-region milestone may add per-region buckets; that is **not** V1 work.)

### Networking (Horde/Lore style)

| Port | Purpose | Default CIDR |
|------|---------|----------------|
| Public API | Clients, Horde, workstations | `allowedCidr` default **`10.0.0.0/8`** |
| Internal API | Reserved for future inter-node use; open in SG for schema stability | `internalCidr` default **`10.0.0.0/8`** |
| Scylla CQL 9042 | Only when `backend=scylla` | DDC instance security group only — never public internet |

Default public port **80** (or AMI’s public port if documented in `docs/ddc-ami.md`); internal default **8080**.

**CIDR warnings (match Horde/Lore):** If `allowedCidr` (or equivalent) is `0.0.0.0/0`, print a **strong WARNING** in dry-run and post-setup output that the DDC public API is open to the internet and that V1 has **no** OIDC/JWT — restrict to a private/VPN CIDR for any real use. Same spirit as horde/workstation `0.0.0.0/0` warnings.

Probe: `GET http://<host>:<publicPort>/health/ready`.

**Auth V1:** SG CIDR only. No OIDC/JWT/ALB/HTTPS termination.

### Integration (soft only)

| Module | V1 |
|--------|-----|
| Horde / CI / workstation | Print `UE-CloudDataCacheHost` / sample curl in Next steps; optional mention if those modules exist in state |
| Lore / Perforce | None |

Endpoints file: `.fabrica/ddc-endpoints.yaml` (mode 0600) — public URL, namespace, backend kind. Single host. Not secrets unless auth is added later.

### State

Module name: `ddc`.

```json
{
  "name": "ddc",
  "version": "<amiId>",
  "status": "ready",
  "resources": [
    {"type": "AWS::IAM::Role", "id": "..."},
    {"type": "AWS::S3::Bucket", "id": "...", "properties": {"region": "us-east-1", "role": "blob"}},
    {"type": "AWS::EC2::SecurityGroup", "id": "..."},
    {"type": "AWS::EC2::Instance", "id": "i-...", "properties": {
      "region": "us-east-1",
      "role": "coordinator",
      "instanceType": "m7i.xlarge",
      "volumeSize": "500"
    }}
  ]
}
```

Always set `Properties.region` (home region) and `Properties.role` (`coordinator` for the DDC host; `scylla` for optional Scylla; `blob` for bucket). Co-located edge role may be implied by topology/plan rather than a second resource row — one EC2 resource for the DDC service.

No `regions[]` array of remote deployments in V1 state.

### Cost

- Reuse shared EC2/EBS estimators in `internal/perforce/cost.go` — **do not re-register** those TypeNames.
- Register S3/IAM estimators only if not already global.
- `costsource` case `"ddc"`: DDC instance + optional Scylla + volumes + bucket; prefer state Properties over config.

### Config (`internal/config`)

```yaml
ddc:
  backend: zen                 # default: zen. scylla = optional advanced (1-node, not HA)
  amiId: ami-...               # required — Unreal Cloud DDC AMI
  scyllaAmiId: ""              # required only when backend=scylla
  instanceType: m7i.xlarge
  volumeSize: 500
  scyllaInstanceType: i4i.large
  scyllaVolumeSize: 500
  vpcId: ""
  subnetId: ""
  allowedCidr: 10.0.0.0/8      # warn strongly if 0.0.0.0/0
  internalCidr: 10.0.0.0/8
  publicPort: 80
  internalPort: 8080
  bucket: ""                   # default fabrica-ddc-<account>-<region>
  namespace: deriveddatacache
```

### Target tree

```
cmd/ddc/
  ddc.go
  setup/
  status/
  destroy/

internal/ddc/
  plan.go
  resources.go
  userdata.go
  backend.go
  cost.go
  endpoints.go

internal/topology/
  topology.go

docs/ddc-ami.md
```

**Not in tree for V1:** `cmd/ddc/region/`, multi-region provider factory, replication peer updaters.

---

## Key decisions & trade-offs

| Topic | Choice | Trade-off |
|-------|--------|-----------|
| Single-region only | No multi-region CLI or runtime | Defers geographic cache; keeps V1 shippable |
| One EC2, two roles in model | Co-located | Cheap; future can split hosts using same Role vocabulary |
| Default `zen` | Scylla opt-in with warnings | Most users avoid ops-heavy DB path |
| Scylla 1-node | Advanced bootstrap only | Not HA; documented loudly |
| `setup` not `create` | Aligns with ci/deploy | Slight name difference vs perforce/lore |
| Non-empty S3 refuse delete | Protect cache data | Orphan bucket cost until emptied |
| No OIDC | SG + CIDR warning | Unsafe if opened to the world |

---

## Out of scope (V1) — explicit

**Multi-region (entire category deferred):**

- `fabrica ddc region add` (or any region subcommand)
- Additional edge instances or remote regions
- Replication peers, speculative/on-demand replication config, peer URL management
- Per-region buckets beyond the single home-region bucket
- Multi-region Cloud Control / `ForRegion` provider work driven by this module
- Status output for “edges in other regions”

**Also out of scope:**

- Client mounts, DefaultEngine.ini / Ludus auto-config  
- OIDC, JWT, Okta, ALB, ACM, public HTTPS termination  
- EKS, Helm, Kubernetes  
- Scylla RF=3, multi-DC keyspace automation, production Scylla clusters  
- Advanced replication policies  
- Multi-cloud providers (GCP/Azure)  
- Godot / Unity Accelerator  
- Grafana/Datadog dashboards  
- `ddc ami build`  
- Auto-scaling, Spot fleets  
- Hard dependency on Horde being provisioned first  

---

## Future expansion (not V1 implementation)

A later milestone may add `ddc region add`, remote edges, per-region buckets, and replication. That work should **extend** `internal/topology` and state `role`/`region` properties — not fork the module. **None of that code ships in V1.**

---

## Verification (design-level)

- `ddc setup --dry-run` → single-host plan + monthly cost; `0.0.0.0/0` warning if applicable; scylla warnings if backend=scylla  
- Setup idempotent; partial failure recoverable  
- `ddc status --probe` → ready on healthy health endpoint  
- `ddc destroy` reverse-order; non-empty S3 safe  
- `destroy --all` includes `ddc` in locked order  
- `cost report` includes `ddc`  
- E2E: setup → status → destroy on fake provider  
- Layering check clean; `internal/ddc` SDK-free  
- **No** region-add command, peer config, or multi-region resource loops in the codebase  

---

## References

- Epic: [Cloud-type Derived Data Cache](https://dev.epicgames.com/documentation/unreal-engine/how-to-set-up-a-cloud-type-derived-data-cache-for-unreal-engine)  
- Fabrica: Lore design, Horde V1, Perforce module patterns  
- Product: `Fabrica_PRODUCT_SPEC.md`  
- Implementation plan: `docs/superpowers/plans/2026-07-15-distributed-ddc.md`  
