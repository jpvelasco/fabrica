# Workstation DCV AMI runbook

`fabrica workstation create` is **AMI-first**. A stock Ubuntu image is not a
NICE DCV AMI. Create **fails closed** if the AMI name looks like Canonical
Ubuntu 22.04 server (the Phase A E2E failure mode). Fabrica only configures a
DCV virtual session and password via cloud-init; it does not install DCV.

Generate bake artifacts (local, no AWS calls):

```
fabrica workstation ami build --region us-west-2 --base-image ami-REPLACE --output-dir workstation-ami
```

The `--base-image` default is a placeholder ID; always pass a live Ubuntu
22.04 server AMI from your target region (`aws ec2 describe-images` with a
`ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-*` filter, `State`
`available`).

In guardrailed accounts (IAM writes gated on a path + boundary,
`ec2:RunInstances` gated on request tags), set `cloud.aws.iam` and
`cloud.aws.volumeRequestTags` in `fabrica.yaml` before running
`workstation create`; otherwise the role and instance steps fail. See
[guardrailed-accounts.md](guardrailed-accounts.md).

## Contract

| Requirement | Detail |
|-------------|--------|
| OS | Ubuntu 22.04 LTS (Jammy), x86_64 |
| Desktop user | `ubuntu` user present — cloud-init creates the DCV session owned by it and sets its login password. Bake with the standard Ubuntu user present; do not remove it. |
| NICE DCV | `dcv` on PATH (2023.0+ CLI surface: `create-session`, `list-sessions`, `set-config`); `dcvserver.service` enabled |
| X session | `xsetroot` (`x11-xserver-utils`), `dbus-launch` (`dbus-x11`), `xdg-user-dirs`, and a terminal emulator (`xterm`). `/etc/X11/Xsession` aborts unless it finds a session manager, a window manager, a terminal emulator, or a user `~/.xsession` / `~/.Xsession`. The bake ships `xterm` so the boot-created session has something to start. A window manager (for example `openbox`) is optional and is not installed. |
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
| 2026-09-20 | us-west-2 | `ami-0219a686f7416f70a` | Image Builder 1.0.1; private subnet, no public IP, SSM Online; `dcv` on PATH; `dcvserver` enabled; instance terminated. **Deregistered** — this row is the last known-good record; a live account may hold no DCV AMI. Bake a fresh one (`workstation ami build`, then Image Builder) before the next `workstation create`. |

Do not mark an AMI known-good from Image Builder success alone.

## Stale `amiId` / `subnetId` in `fabrica.yaml`

`workstation create` reads `workstation.amiId` and `workstation.subnetId`
from `fabrica.yaml`. Both can silently go stale: an AMI deregistered after a
bake, or a VPC rebuilt after a `fabrica destroy` pass. Create fails fast on
both — before any resource is written — so a stale value never launches a
half-broken instance.

- `workstation.amiId` points at a missing (or stock Ubuntu) image:

  ```
  inspecting workstation.amiId ami-0…: describing AMI ami-0…: … InvalidAMIID.NotFound.
  Confirm the AMI exists and is a NICE DCV image (see docs/workstation-ami.md)
  ```

  Bake a fresh AMI with `workstation ami build` (see below) and point
  `workstation.amiId` at the new ID. A stock Canonical Ubuntu AMI is
  rejected by name — it is not a DCV AMI.

- `workstation.subnetId` points at a missing subnet: the EC2 instance step
  fails at apply with `InvalidSubnetID.NotFound`. The subnet is referenced
  only by the instance create, which runs after the security group, IAM
  role, and instance profile, so a stale subnet leaves those three
  recorded in state — `fabrica workstation destroy` (or `fabrica drift`)
  clears them, then fix the subnet and re-run create. Pick a live subnet in
  the account:

  ```
  aws ec2 describe-subnets --filters Name=state,Values=available \
    --query 'Subnets[].SubnetId' --output text
  ```

  Omitting both `vpcId` and `subnetId` lets create resolve the account's
  default VPC instead of pinning one.

The local `fabrica.yaml` is not checked in; keep it in sync with the VPC
after every rebuild.

## Boot verification (session, not just HTTPS 200)

Fabrica's cloud-init targets the current DCV CLI (2023.0+ / 2025.0.x):
`dcv set-config` for the idle timeout, `dcv create-session --type virtual`
for the persistent `workstation` session, and `chpasswd` for the login
password written to `.fabrica/workstation-credentials.yaml` (user `ubuntu`).
The script **fails closed** if the `ubuntu` user is missing or the session
does not appear within 3 minutes. It also starts `dcvserver` **before**
`dcv create-session`: with the daemon stopped, `create-session` exits 0 but
the session is never persisted (verified live on DCV 2025.0.x).

The first Xdcv launch on a cold boot can exceed the session starter's
display-number read window (30s). `list-sessions` may show the session for
a few seconds before the starter tears it down. After the session first
appears, cloud-init waits 45s and checks again. If the session is gone, it
closes any leftover, removes a stale `/tmp/.X11-unix/X0` socket, and runs
`dcv create-session` again, backing off 15s then 30s, for up to three
attempts. A later launch on the same host succeeds once GLX init has warmed.
The script exits 1 if the session never appears, or if it is still gone
after the last attempt (`session was torn down after creation` is not a
successful boot).

## Session password (SSM Parameter Store)

The DCV session password is the OS password for `ubuntu`. DCV authenticates
the session against that OS password, so the instance must learn it at boot.
`workstation create` mints a unique parameter name per create —
`/fabrica/workstation/<create-nonce>/dcv-password`, a 32-hex nonce — and
writes the password to it as a Parameter Store SecureString, tagged
`ManagedBy=fabrica`. There is no shared name and no overwrite: a create
that finds an existing name fails and names the orphan. The password is
also recorded in `.fabrica/workstation-credentials.yaml` (mode `0600`) —
the operator source of truth. The value is not placed in UserData, Cloud
Control desired state, or module state. State records the parameter name
only (`sessionPasswordParameter` on the instance resource).

Cloud-init runs `aws ssm get-parameter --with-decryption` (installing
`awscli` when the AMI does not already have it) and feeds the value to
`chpasswd` via an unquoted heredoc, so the password never appears on a
command line. The instance role may `ssm:GetParameter` on that one
parameter ARN only. A failed or empty read exits 1 before the session is
created.

After the DCV session is created and verified, the script still truncates
the local copies (`user-data.txt`, `user-data.txt.i`, and `user-data`),
re-verifies they are empty, and then wipes `scripts/part-001` — the file
cloud-init is executing — as the very last step. The launch script that
remains readable via IMDS `/latest/user-data` and
`ec2:DescribeInstanceAttribute` contains the parameter name and region, not
the password. The script never enables `set -x`.
`Scrubbed EC2 userdata (local).` marks the point before the part-001 wipe
in `/var/log/cloud-init-output.log`.

`workstation terminate` deletes exactly the name recorded in state and
skips the deletion when nothing is recorded. A create that fails before
the instance exists deletes it too; once the instance exists the parameter
stays until terminate so cloud-init can read it. A missing parameter is
success. The local credentials file is not deleted.

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
