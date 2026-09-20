# Workstation DCV AMI runbook

`fabrica workstation create` is **AMI-first**. A stock Ubuntu image is not a
NICE DCV AMI. Create **fails closed** if the AMI name looks like Canonical
Ubuntu 22.04 server (the Phase A E2E failure mode). Fabrica only configures a
DCV virtual session and password via cloud-init; it does not install DCV.

Generate bake artifacts (local, no AWS calls):

```
fabrica workstation ami build --region us-west-2 --base-image ami-REPLACE --output-dir workstation-ami
```

## Contract

| Requirement | Detail |
|-------------|--------|
| OS | Ubuntu 22.04 LTS (Jammy), x86_64 |
| NICE DCV | `dcv` on PATH; `dcvserver.service` enabled |
| SSM Agent | deb or snap unit enabled (fail closed, same as Lore/Horde) |
| Port | 8443 (HTTPS) opened by Fabrica at create from `workstation.allowedCidr` |

Virtual sessions do not require a GPU. Artist (`g6`) templates still want a
GPU-capable instance type; bake GPU drivers separately if you need them.

## Private path

Verify through SSM (or VPN/in-VPC), not laptop `workstation list` against a
private IP. Endpoint SG inbound TCP 443 from the VPC CIDR: [ssm-private.md](ssm-private.md).

## Known-good AMIs

| Date (UTC) | Region | AMI ID | Notes |
| --- | --- | --- | --- |
| 2026-09-20 | us-west-2 | `ami-0219a686f7416f70a` | Image Builder 1.0.1; private subnet, no public IP, SSM Online; `dcv` on PATH; `dcvserver` enabled; instance terminated |

Do not mark an AMI known-good from Image Builder success alone.

## Known limitation: DCV CLI mismatch in cloud-init

The AMI row above ships NICE DCV 2025.0.x, whose `dcv` CLI no longer exposes
the `configure-session` / `configure` subcommands. Fabrica's cloud-init
(`internal/workstation/userdata.go`) still calls them first under
`set -euo pipefail`, so the script aborts before it creates the persistent
session and sets the generated DCV password. The DCV server itself starts and
stays reachable on 8443, and the AMI passes the `dcv`-presence gate — but the
Fabrica-provisioned session and password are not applied until the cloud-init
script is updated for the current DCV CLI. Verify session creation over SSM
(`dcv list-sessions`) when baking a new AMI; do not treat HTTPS 200 on 8443 as
proof the session setup ran.
