# Building a Lore AMI

`fabrica lore create` requires an AMI (`lore.amiId` in `fabrica.yaml`) that already
contains the `loreserver` binary (and optional systemd unit). Fabrica's cloud-init
only mounts the EBS store, writes local store config, and starts the service — it
does not build or install Lore.

Lore is pre-1.0; re-check [Epic's Lore docs](https://epicgames.github.io/lore) when
pinning a server version.

---

## Requirements

| Requirement | Detail |
|-------------|--------|
| **OS** | Linux x86_64 (Ubuntu 22.04 LTS recommended; cloud-init targets bash + systemd) |
| **loreserver** | On `PATH` as `loreserver`, **or** a `loreserver.service` systemd unit |
| **Config** | Service should accept `loreserver --config <DIR>` (Fabrica uses `/etc/loreserver`) |
| **Store** | Fabrica mounts EBS at `/opt/loreserver/store` and writes local store paths under it |
| **Ports** | Process listens on **41337** (TCP gRPC + UDP QUIC) and **41339** (HTTP, `GET /health_check`) |
| **Architecture** | `x86_64` (Graviton needs special Lore build flags; not validated in V1) |

At boot, Fabrica's cloud-init script will:

1. Wait for / format / mount the data volume at `/opt/loreserver/store`
2. Write `/etc/loreserver/local.toml` with local immutable/mutable/lock store paths
3. Start `loreserver` via systemd if present, else `loreserver --config /etc/loreserver`
4. Leave `/var/log/fabrica-lore-init.log` for debugging

V1 uses **self-signed TLS** and **no JWT**. Network access is controlled by
`lore.allowedCidr` on the security group.

---

## Option 1: Official Dockerfile (reference)

Epic ships `lore-server/Dockerfile` in the [Lore repo](https://github.com/EpicGames/lore).
Use it as a recipe for what to install on a bake instance, then create an AMI from
that instance (or use Image Builder separately — `fabrica lore ami build` is **not**
in V1).

Expected ports in the official image: **41337** tcp+udp and **41339**.

---

## Option 2: Manual bake

1. Launch Ubuntu 22.04 EC2 (same family you plan to run, e.g. m5.xlarge).
2. Install a released `loreserver` binary (or build from source per Lore docs).
3. Optionally install a systemd unit that runs `loreserver --config /etc/loreserver`.
4. Open nothing public; stop the instance and create an AMI.
5. Set `lore.amiId: ami-...` in `fabrica.yaml` and run `fabrica lore create`.

---

## Verification after create

```bash
fabrica lore status -w
# From a host that can reach the private IP:
curl -sS "http://<private-ip>:41339/health_check"
```

If readiness fails within ~10 minutes, SSH (if your AMI allows) and check:

```text
/var/log/fabrica-lore-init.log
/var/log/loreserver.log
systemctl status loreserver
```
