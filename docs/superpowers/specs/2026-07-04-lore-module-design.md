# Lore Module — Design (Phase 2 pull-forward: production VCS provisioning)

Status: **Approved** (2026-07-15) — ready for implementation
Date: 2026-07-04 (research); approved 2026-07-15

## What Lore is (researched 2026-07-04)

[Lore](https://github.com/EpicGames/lore) is Epic Games' new open-source,
next-generation version control system (MIT, ~81% Rust, pre-1.0). It's a
**centralized, content-addressed VCS optimized for code + large binary assets** —
effectively a modern alternative to Perforce Helix Core for game/entertainment
teams. It uses Merkle-tree content addressing, chunked storage, deduplication,
and sparse on-demand workspaces. It ships in UEFN.

**This is the "Lore support (production server management)" line already parked
in the ROADMAP Phase 2+ list — pulled forward at JP's request.**

### Concrete server facts (from epicgames.github.io/lore + the repo)

- **Server binary:** `loreserver`, run as `loreserver --config <DIR>` (config dir
  holds `local.toml` + selected `<env>.toml`, layered over a built-in
  `default.toml`; `LORE__`-prefixed env vars override).
- **Ports:**
  - `41337` — **gRPC over TCP** AND **QUIC over UDP** (same port, both protocols; UDP mapping is mandatory for QUIC).
  - `41339` — **HTTP** (has a `GET /health_check` → `200 OK` readiness endpoint).
  - `41340` — internal gRPC/QUIC (disabled by default).
- **Storage backends** (`[immutable_store]` / `[mutable_store]` / `[lock_store]`,
  each `mode = local|composite|replicated|remote|plugin`):
  - `local` → a filesystem `path` (default under temp; the deploy guide uses
    `/opt/loreserver/store`).
  - Lore ships a **`lore-aws` crate supporting S3 + DynamoDB** — so remote/backed
    stores on AWS object storage are a first-class Lore capability.
- **TLS:** QUIC needs a cert (`[server.quic.certificate]` cert_file/pkey_file);
  if omitted, the server self-signs for localhost. There's a
  `scripts/server/make-certs.sh` helper.
- **Auth:** optional JWT (`[server.auth]` jwt_issuer/audience/jwk.endpoint);
  skipped if unset.
- **Official Dockerfile** exists (`lore-server/Dockerfile`) — builds `loreserver`
  + self-signed certs, runs with `/data` as the store, ports 41337 tcp+udp +
  41339. Notably: Graviton/arm64 has special compiler flags; the Docker path
  targets `linux/amd64`.

## Why this fits Fabrica cleanly

Lore-the-server is **structurally almost identical to the `perforce` module**: a
single long-running VCS server on an EC2 instance behind a security group,
provisioned AMI-first (the AMI carries the `loreserver` binary + deps),
configured + started via cloud-init, with credentials/certs written locally, a
readiness probe, and reverse-order teardown. `perforce` is the canonical
template in CLAUDE.md; Lore is a second instance of that exact shape. That makes
this a well-understood build, not a novel one.

**The one genuinely new wrinkle vs. perforce:** the **QUIC/UDP** port. Perforce
is pure TCP (1666); Lore needs a security-group rule for **UDP 41337** in
addition to TCP 41337 and TCP 41339. The Cloud Control SG desired-state must
include a UDP ingress rule — a small but real difference from every existing
Fabrica module (all TCP-only today).

## Proposed module surface

Mirror perforce/horde. `fabrica lore <subcommand>`:

```
fabrica lore create     # provision SG + EC2 (AMI-first, loreserver) + cloud-init config/start
fabrica lore status     # state + Cloud Control live data; HTTP /health_check probe on 41339; provisioning→ready
fabrica lore destroy     # reverse-order teardown (instance → SG); via cmd/internal/teardown engine
fabrica lore ami build   # (optional, phase 2 of this work) generate an Image Builder recipe for a loreserver AMI
```

V1 of this module = `create` / `status` / `destroy` (the perforce triad). `ami
build` is a natural follow-on mirroring `horde ami build` but is **out of scope
for the first cut** unless JP wants it bundled.

### Architecture (follows the locked module pattern)

- **`internal/lore/` — pure plan layer** (no AWS SDK): `CreatePlan`,
  `SGDesiredState` (now with a UDP rule — see below), `InstanceDesiredState`,
  cloud-init generator (`Generate`/`GenerateRaw`), cost estimators, `VPCResolver`
  interface. Reuses the shared EC2/EBS cost estimators in
  `internal/perforce/cost.go` (do NOT re-register `AWS::EC2::Instance`/`Volume`).
- **`cmd/lore/{create,status,destroy}`** — Cobra commands with `RuntimeSource`/
  `OptionsSource` closures + seam fields, exactly like `cmd/perforce/*`. `destroy`
  uses the `cmd/internal/teardown` engine (EC2→SG default order — no ResourceOrder
  hook needed; it's the same EC2/SG pair as perforce). `status` uses the
  `cmd/internal/modstatus` engine with a Lore-specific `Renderer`.
- **`LoreConfig`** added to `internal/config/config.go` (NOT `internal/lore/`),
  `mapstructure` tags — same rule as every other module.
- **Wire** `cmd/lore` into `cmd/root/root.go`.
- **Two-file tests** per command package (white-box `*_test.go` +
  black-box `cobra_test.go`), ≥90% patch coverage, no-0%-function rule.
- **E2E:** add a `test/e2e/lore_test.go` flow (create→status→destroy) against the
  fake provider — the fake already handles the EC2/SG shape; the UDP SG rule is
  just desired-state JSON, provider-agnostic.
- **README + doc-drift guard:** the guard will REQUIRE `lore create/status/
  destroy` to be documented (that's its job) — add a `### Lore` README section.
- **CLAUDE.md / ROADMAP:** add the module to the tables; move "Lore support" from
  Phase 2+ to done.

### Security group — the UDP nuance (design detail)

`internal/lore/SGDesiredState` must emit ingress for:
- TCP 41337 (gRPC), UDP 41337 (QUIC), TCP 41339 (HTTP).

Cloud Control `AWS::EC2::SecurityGroup` `SecurityGroupIngress` entries take an
`IpProtocol` field — `"tcp"` / `"udp"`. This is the first Fabrica module to emit
a `udp` rule; verify the desired-state JSON shape against the existing
perforce/horde SG builders and add the UDP entry. Default `allowedCidr` follows
the horde/workstation convention (config-driven, warn on `0.0.0.0/0`).

### Storage decision (the real design question for JP)

Lore's durable store can be **local disk (EBS)** or **S3-backed** (via
`lore-aws`). Two options:

1. **V1 = local/EBS store** (RECOMMENDED for the first cut). The `loreserver`
   writes its immutable+mutable+lock stores to an EBS data volume (like
   perforce's depot volume). Simplest, matches the perforce module exactly, one
   fewer moving part. Cloud-init sets `[immutable_store.local] path=...` etc. to
   the mounted volume. A future enhancement points the store at S3.
2. **V1 = S3-backed store.** Provision an S3 bucket + point Lore's remote store at
   it. More "cloud-native" and durable, but: more resources, IAM role for bucket
   access, and Lore's remote-store config is less documented than local — more
   integration risk for a first cut.

**Recommendation: option 1 (local/EBS) for V1**, with an S3-backed store as a
documented follow-up. Rationale: get a working, tested Lore module out following
the proven perforce pattern; add S3 durability once the basics are solid. JP to
confirm.

### TLS + auth (V1 scope)

- **TLS:** V1 uses the server's **self-signed cert** path (cloud-init runs
  `make-certs.sh` or lets loreserver self-sign). Real CA certs = follow-up.
  Document that clients must trust the self-signed cert / use `--insecure`-style
  client config (verify Lore client's exact flag).
- **Auth (JWT):** V1 leaves auth **unset** (Lore skips JWT verification when
  unconfigured) — same posture as horde's optional token. Network is restricted
  by the SG `allowedCidr`. JWT/JWKS integration = documented follow-up.

### AMI-first (like horde/workstation)

Lore's `loreserver` is a Rust binary with build complexity (Graviton flags,
etc.), so building it at instance-boot is wrong. **AMI-first:** the AMI must
already contain the `loreserver` binary (+ certs tooling). Fabrica's cloud-init
only *configures* (`local.toml`) and *starts* the service — exactly the
horde/workstation model. `lore.amiId` is required config, like `horde.amiId`.
(A `lore ami build` command generating an Image Builder recipe is the natural
follow-on, mirroring `horde ami build`.)

## Open questions — locked (2026-07-15)

1. **Storage:** **local/EBS for V1** (S3-backed store is a documented follow-up).
2. **Scope of first cut:** **`create` / `status` / `destroy` only** (`lore ami build` is out of V1).
3. **Relationship to perforce:** **parallel VCS options** — both modules coexist; no forced migration.
4. **Milestone placement:** **v0.1 ships without Lore; Lore is v0.2.**
5. **Client-side:** **server provisioning only** — no mount/workspace helpers in V1.

Multi-region / edge topology is **not** in V1.

## Scope boundary (what this spec is NOT)

- Not the Lore *client* or workspace management — Fabrica provisions the server.
- Not S3-backed storage in V1 (recommended follow-up).
- Not JWT auth / CA TLS in V1 (recommended follow-up).
- Not `lore ami build` in V1 unless JP bundles it (Q2).
- Not a replacement of the perforce module (Q3).

## Rough build order (once approved — for the eventual plan)

1. `internal/lore` plan layer (CreatePlan, SG desired-state **with UDP rule**,
   instance desired-state, cloud-init, cost) + unit tests.
2. `LoreConfig` in `internal/config`.
3. `cmd/lore/create` (+ two-file tests).
4. `cmd/lore/status` via modstatus engine + HTTP `/health_check` probe (+ tests).
5. `cmd/lore/destroy` via teardown engine (+ tests).
6. Wire into root; `test/e2e/lore_test.go` flow.
7. README `### Lore` section (doc-drift guard enforces it); CLAUDE.md + ROADMAP.

---

**Note to JP:** this is a research-backed draft, not an approved spec. The five
open questions (esp. storage backend + whether Lore blocks or follows the v0.1
release) genuinely change the shape — let's settle those before I turn this into
an implementation plan. Everything here is grounded in the real Lore docs/repo as
of 2026-07-04, but Lore is pre-1.0 (APIs/formats may shift), so we should re-pin
the server facts at implementation time.
