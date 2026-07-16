# Unreal Cloud DDC AMI requirements

`fabrica ddc setup` is **AMI-first**. The AMI must already contain Unreal Cloud DDC (Jupiter) and (for `backend: scylla`) a Scylla Open Source install. Fabrica only mounts the hot EBS volume, writes config env under `/etc/unreal-cloud-ddc/fabrica.env`, and starts the service unit.

V1 is **single home-region only**. There is no multi-region edge or replication peer automation.

## Required (zen — default)

- Unreal Cloud DDC (Jupiter) binary or container entrypoint
- systemd unit named `unreal-cloud-ddc` (enable/start from cloud-init)
- Ability to read S3 with the instance profile (Fabrica attaches S3 RW + SSM core)
- Health endpoints on the public API port:
  - `GET /health/ready`
  - `GET /health/live`
- Local store path for hot tier (default `/opt/unreal-cloud-ddc/store` on the data volume)

## Optional Scylla path

When `ddc.backend: scylla` (or `--backend scylla`):

- **Separate AMI** via `ddc.scyllaAmiId` for a **single-node** Scylla bootstrap host
- Unit name `scylla-server`
- **Not production HA** — no RF=3, no multi-DC. Prefer `zen` unless you explicitly need Scylla and accept the limitations.

## Cloud-init contract

Fabrica writes `/etc/unreal-cloud-ddc/fabrica.env` with:

| Variable | Purpose |
|----------|---------|
| `FABRICA_DDC_BUCKET` | S3 blob bucket |
| `FABRICA_DDC_REGION` | Home AWS region |
| `FABRICA_DDC_NAMESPACE` | Default namespace |
| `FABRICA_DDC_PUBLIC_PORT` | Public API port (default 80) |
| `FABRICA_DDC_INTERNAL_PORT` | Internal API port (default 8080) |
| `FABRICA_DDC_BACKEND` | `zen` or `scylla` |
| `FABRICA_DDC_STORE` | Mounted hot store path |
| `FABRICA_DDC_SCYLLA_CONTACT` | Optional contact (often empty at first boot) |

Your AMI’s unit should source this file and configure Jupiter accordingly. **No remote replication peer list is written in V1.**

## Ports

| Port | Role | Default CIDR |
|------|------|----------------|
| Public API | Clients / Horde | `allowedCidr` (default `10.0.0.0/8`) |
| Internal API | Reserved | `internalCidr` |
| 9042 | Scylla CQL (scylla backend only) | `internalCidr` |

Warn if `allowedCidr` is `0.0.0.0/0` — V1 has no OIDC.

## References

- Epic: [Cloud-type Derived Data Cache](https://dev.epicgames.com/documentation/unreal-engine/how-to-set-up-a-cloud-type-derived-data-cache-for-unreal-engine)
- Design: `docs/superpowers/specs/2026-07-15-distributed-ddc-design.md`
