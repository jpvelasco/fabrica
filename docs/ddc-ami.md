# Unreal Cloud DDC AMI requirements

`fabrica ddc setup` is **AMI-first**. Generate a bake guide with `fabrica ddc ami build` (local files only). The AMI must already contain Unreal Cloud DDC (Jupiter) and (for `backend: scylla`) a Scylla Open Source install. Fabrica only mounts the hot EBS volume, writes config env under `/etc/unreal-cloud-ddc/fabrica.env`, and starts the service unit.

**Multi-region edges:** `fabrica ddc region add REGION` provisions an edge node from the **same** AMI shape, but AMIs are region-specific — the image used in the home region does not exist in the edge region. Copy it first:

```
aws ec2 copy-image --source-region us-east-1 --region eu-west-1 \
  --name ddc-edge-eu-west-1
```

Then pass the copy (or set `ddc.amiId` in `fabrica.yaml`) when adding the region. Edge nodes reuse the home blob bucket and the global instance profile; there is no replication-peer automation — peer/replication wiring is operator-managed.

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
- **Production overlay:** `ddc.scylla.nodes>=3` documents RF=3 cost and `fabrica ddc topology` prints the plan. V1 still provisions one Scylla host; extra nodes stay operator-built.
- Set `ddc.replication.enabled` and `peers` to write `FABRICA_DDC_REPLICATION_PEERS` into `fabrica.env`. Fabrica does not open inter-region sockets.

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

Your AMI’s unit should source this file and configure Jupiter accordingly. **No remote replication peer list is written — replication is operator-managed.**

## Ports

| Port | Role | Default CIDR |
|------|------|----------------|
| Public API | Clients / Horde | `allowedCidr` (default `10.0.0.0/8`) |
| Internal API | Reserved | `internalCidr` |
| 9042 | Scylla CQL (scylla backend only) | `internalCidr` |

When `ddc.oidc.enabled` is true, cloud-init also writes `FABRICA_DDC_OIDC_ENABLED`, `FABRICA_DDC_OIDC_ISSUER`, `FABRICA_DDC_OIDC_CLIENT_ID`, `FABRICA_DDC_OIDC_AUDIENCE`, and `FABRICA_DDC_OIDC_REDIRECT`. The AMI must consume those; Fabrica does not provision the identity provider. Disabled OIDC (default) emits none of those variables, so CIDR/static auth stays unchanged.

Warn if `allowedCidr` is `0.0.0.0/0` without OIDC.

## Private-subnet bake path (runbook)

Fabrica's DDC runtime consumes only an AMI ID; it never installs Jupiter.
This runbook mirrors the [Lore AMI runbook](lore-ami.md) so a DDC AMI can be
baked and verified the same way: private-subnet Image Builder bake, then
SSM-verified `ddc setup`/`status`/`destroy` before the AMI is trusted.

> **Verification status:** no DDC/Jupiter AMI is known-good yet. The
> known-good table below intentionally has no pre-filled row. A successful
> Image Builder build by itself is not proof that an AMI works with Fabrica.

### Contract recap (what the bake must produce)

| Requirement | Contract |
| --- | --- |
| Base OS | Ubuntu 22.04 LTS (Jammy), x86_64, region-local base AMI. |
| DDC payload | Studio-supplied Unreal Cloud DDC (Jupiter) distribution or container entrypoint; license-sensitive — Fabrica does not download it. |
| Service | systemd unit `unreal-cloud-ddc` present and **enabled**. Cloud-init does `systemctl enable && restart` (start fallback), so the unit must exist in the AMI. |
| Config | The unit sources `/etc/unreal-cloud-ddc/fabrica.env`; the AMI must not bake its own generated config or credentials. |
| Health | `GET /health/ready` and `GET /health/live` on the public port (default 80). `fabrica ddc status` live-probes `/health/ready` per region. |
| Scylla (optional) | For `backend: scylla` use a **separate** AMI (`ddc.scyllaAmiId`) containing Scylla Open Source with the `scylla-server` unit enabled; single-node bootstrap only, never HA. |
| Management | SSM agent installed and enabled (private E2E verifies through SSM, not laptop probes). |
| Secrets | No credentials, tokens, store data, or studio content baked into the image. |

### Bake (Image Builder, preferred)

`fabrica ddc ami build` is local-only (writes `build-guide.md` only). Drive
Image Builder directly, using the same boto3-based flow proven by the Horde
and Lore bakes in this account:

1. Stage the Jupiter distribution in a **private** S3 prefix (least-privilege
   read for the bake worker).
2. Create an Image Builder component (JSON AWSTOE document,
   `phases[].steps[]` with `ExecuteBash` actions; **no `{{...}}` anywhere in
   the script or comments** — AWSTOE parses those as variable refs) that
   syncs the payload, installs Jupiter, writes and enables the
   `unreal-cloud-ddc` unit, and enables the SSM agent **fail-closed** (a
   missing SSM agent must abort the bake).
3. Create the image recipe with `semanticVersion` and components referenced
   by **build-version ARN** only (`.../component/<name>/<ver>/1`).
4. Start the build with `create_image(imageRecipeArn,
   infrastructureConfigurationArn, tags)` — there is no `start_image_build`
   API. Track it with `get_image(imageBuildVersionArn=...)` and poll
   `state.status` to `AVAILABLE` (not `SUCCESS`); the AMI id is in
   `outputResources.amis[0].image`. Tag the build
   `ManagedBy=fabrica`/`FabricaModule=ddc` so it is visible to sweeps.
5. Poll to a terminal state and clean up failed candidates — never reuse a
   failed bake as known-good.

For `backend: scylla`, repeat the same flow against a base that includes
Scylla Open Source; the resulting AMI is recorded under `ddc.scyllaAmiId`,
not `ddc.amiId`.

### Verification checklist (private E2E gate)

Run in a private subnet with an SSM instance profile (SSM endpoints per
[ssm-private.md](ssm-private.md)); `fabrica ddc status` probes the private IP
and cannot succeed from a laptop outside the VPC.

1. Set `ddc.amiId` (and `ddc.scyllaAmiId` for the scylla path) in
   `fabrica.yaml` with the candidate AMI.
2. `fabrica ddc setup --yes` — plan + cost review, then approve.
3. SSM: instance `PingStatus` → `Online`; `unreal-cloud-ddc` (and
   `scylla-server` for the scylla path) active;
   `GET /health/ready` and `GET /health/live` 200 on loopback via SSM.
4. `fabrica ddc status --wait` — home region (and any edge regions added)
   report ready.
5. `fabrica ddc destroy` (or `region destroy` for edges) — clean teardown of
   instance, SG, blob bucket, and instance profile.
6. Add the row to the known-good table **only after all steps pass**; record
   the base AMI, the exact Jupiter source revision, and the evidence link.
   Edge regions reuse the AMI only after `aws ec2 copy-image` — the table
   row is per-region.

## Known-Good AMIs

Fill this table only after the complete checklist passes. AMIs are
region-specific.

| Date (UTC) | Region | AMI ID | Base AMI | Jupiter source revision | Backend | SSM: setup/status/destroy | Evidence link |
| --- | --- | --- | --- | --- | --- | --- | --- |
| _none yet_ | | | | | | | |

## References

- Epic: [Cloud-type Derived Data Cache](https://dev.epicgames.com/documentation/unreal-engine/how-to-set-up-a-cloud-type-derived-data-cache-for-unreal-engine)
