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
| — | — | TBD | Record after bake + private SSM + DCV `dcv list-sessions` via SSM + terminate |

Do not mark an AMI known-good from Image Builder success alone.
