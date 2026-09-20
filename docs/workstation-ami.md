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
| Desktop user | `ubuntu` user present — cloud-init creates the DCV session owned by it and sets its login password. Bake with the standard Ubuntu user present; do not remove it. |
| NICE DCV | `dcv` on PATH (2023.0+ CLI surface: `create-session`, `list-sessions`, `set-config`); `dcvserver.service` enabled |
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

## Boot verification (session, not just HTTPS 200)

Fabrica's cloud-init targets the current DCV CLI (2023.0+ / 2025.0.x):
`dcv set-config` for the idle timeout, `dcv create-session --type virtual`
for the persistent `workstation` session, and `chpasswd` for the login
password written to `.fabrica/workstation-credentials.yaml` (user `ubuntu`).
The script **fails closed** if the `ubuntu` user is missing or the session
does not appear within 3 minutes. It also starts `dcvserver` **before**
`dcv create-session`: with the daemon stopped, `create-session` exits 0 but
the session is never persisted (verified live on DCV 2025.0.x).

HTTPS 200 on 8443 is **not** proof the session setup ran — `dcvserver` starts
and serves even when cloud-init aborts. Verify the session over SSM (or
VPN/in-VPC) after `workstation create`:

```bash
# Session exists and is owned by the session user
aws ssm send-command --target "i-<instance-id>" --document-name AWS-RunShellScript \
  --comment verify-dcv-session \
  --parameters commands='dcv list-sessions -j'
# expect a session with session-id "workstation" and owner "ubuntu"

# Cloud-init completed
aws ssm send-command ... --parameters commands='tail -n 30 /var/log/cloud-init-output.log'
```

If the session is missing, read `/var/log/cloud-init-output.log` over SSM —
the script prints the failed step with an `ERROR:` line.
