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
`workstation create` writes it to `.fabrica/workstation-credentials.yaml`
(mode `0600`) — the operator source of truth — and to one Parameter Store
SecureString, `/fabrica/workstation/dcv-password`, tagged `ManagedBy=fabrica`.
The value is not placed in UserData, Cloud Control desired state, or module
state. State records the parameter name only (`sessionPasswordParameter`).

Cloud-init runs `aws ssm get-parameter --with-decryption` (installing
`awscli` when the AMI does not already have it) and pipes the value to
`chpasswd`. The instance role may `ssm:GetParameter` on that parameter ARN
only. A failed or empty read exits 1 before the session is created.

After the DCV session is created and verified, the script still truncates
the local copies (`user-data.txt`, `user-data.txt.i`, and `user-data`),
re-verifies they are empty, and then wipes `scripts/part-001` — the file
cloud-init is executing — as the very last step. The launch script that
remains readable via IMDS `/latest/user-data` and
`ec2:DescribeInstanceAttribute` contains the parameter name and region, not
the password. The script never enables `set -x`.
`Scrubbed EC2 userdata (local).` marks the point before the part-001 wipe
in `/var/log/cloud-init-output.log`.

`workstation terminate` deletes the parameter. A create that fails before
the instance exists deletes it too; once the instance exists the parameter
stays until terminate so cloud-init can read it. A missing parameter is
success. A parameter left by an earlier attempt is overwritten on the next
create. The local credentials file is not deleted.

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
