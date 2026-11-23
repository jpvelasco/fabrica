# Building a Dedicated Horde Agent AMI

`fabrica horde agents create` requires a **dedicated agent AMI** (`horde.agents.amiId`
in `fabrica.yaml` or `--ami-id` flag) that contains the Unreal Horde **agent** software —
not the full coordinator/server stack. The agent's only job is to connect to the
coordinator and accept build work.

**This is not the coordinator AMI.** The coordinator AMI (`horde.amiId`) runs MongoDB,
Redis, and the full Horde server. The agent AMI runs only the agent process. Do not
use the coordinator AMI as the agent AMI in production — it wastes compute, storage,
and introduces unnecessary services on every builder node.

Fabrica's cloud-init script only configures the agent to point at the coordinator —
it does not install any software. The AMI must contain the agent binary pre-baked.
Fabrica configures every agent, HTTP or TLS, through the stock native environment
contract (`Horde__Server` and `Horde__ServerProfiles__fabrica__*`).

See [docs/horde-ami.md](horde-ami.md) for the **coordinator** AMI build guide.

For TLS, also follow the [native agent contract](horde-tls.md#native-agent-ami)
for trust placement. Configuration success does not prove enrollment: agents
wait in Horde for approval before they receive work. Run
`fabrica horde agents approve` to list pending agents, then approve them with
`--host <name>` or `--all`.

---

## Generate the agent AMI (recommended)

`fabrica horde ami build --role agent` generates the Image Builder component,
recipe, and build guide that bake the stock Horde Linux agent under the
[native agent contract](horde-tls.md#native-agent-ami). Use it instead of the
manual steps below, which remain as reference.

```bash
# 1. Download the stock agent package once, hash it, stage it in S3
curl https://<coordinator>/api/v1/agentsoftware/default/zip --output HordeAgent.zip -H "Authorization: Bearer <AUTH-TOKEN>"
sha256sum HordeAgent.zip
aws s3 cp HordeAgent.zip s3://my-bucket/horde/HordeAgent.zip

# 2. Generate the agent build files
fabrica horde ami build --role agent --install native --horde-version 5.8.4 \
  --agent-artifact s3://my-bucket/horde/HordeAgent.zip \
  --agent-sha256 <64-hex-from-step-1> \
  --output-dir horde-agent-ami --include-packer
```

The `<AUTH-TOKEN>` is on the coordinator's `/account` page ("Get agent software
download token"). An unauthenticated coordinator needs no `Authorization` header.

Then follow `horde-agent-ami/build-guide.md`. The generator enforces:

- `--install native`, an `s3://…zip` URI, and a 64-hex `--agent-sha256`. There is no default hash. A `.tar.gz` is rejected: Horde 5.8.4 publishes the agent as a zip.
- `HordeAgent.zip` is checked with `sha256sum -c` before anything is unpacked, then `unzip`ed root-owned under `/opt/horde-agent` with no group or other write bits.
- The .NET 10 ASP.NET Core runtime (`aspnetcore-runtime-10.0`) from Canonical's dotnet backports PPA, pinned by fingerprint. The Microsoft apt feed has no .NET 10 for Ubuntu 22.04.
- A nologin `horde-agent` service user, with `/opt/horde-agent/Data` and `/var/lib/horde-agent/.local/share` as the only writable paths.
- `horde-agent.service` runs as `User=horde-agent` with `NoNewPrivileges=true`, `ProtectSystem=strict`, and `ReadWritePaths=` those two paths. It is enabled but not started at bake time.
- The contract marker `/etc/horde/fabrica-native-agent-contract` (contents `1`).
- A validate phase that checks `HordeAgent.dll` exists, that the service user cannot write it, and that `/usr/bin/dotnet --list-runtimes` lists a `Microsoft.NETCore.App 10.x` runtime. It also runs as the service user (`runuser -u horde-agent -- test -w …`), plus `systemctl is-enabled` and `! systemctl is-active`.

The unit runs `ExecStart=/usr/bin/dotnet /opt/horde-agent/HordeAgent.dll -WorkingDir=/opt/horde-agent/Data`
from `WorkingDirectory=/opt/horde-agent/Data`. This is the framework-dependent
5.8.4 layout that Epic's agent guide documents; the stock package has no native executable.

Set the resulting AMI as `horde.agents.amiId` in `fabrica.yaml`.

---

## Requirements

The AMI must meet all of the following:

| Requirement | Detail |
|-------------|--------|
| **OS** | Ubuntu 22.04 LTS (jammy) — cloud-init script targets Ubuntu |
| **Horde agent** | The stock Horde agent (`HordeAgent.zip` unzipped to `/opt/horde-agent`, with `HordeAgent.dll`) running on the .NET 10 runtime (`/usr/bin/dotnet`) |
| **SSM Agent** | `amazon-ssm-agent` installed and enabled (deb `amazon-ssm-agent.service` or snap `snap.amazon-ssm-agent.amazon-ssm-agent.service`). Bake must **fail closed** if the unit cannot be enabled. Required for Session Manager access on private subnets. |
| **Architecture** | `x86_64` (matches default `c7i.xlarge` instance type) |
| **No full server** | The AMI should NOT include MongoDB, Redis, or the Horde coordinator server binary — only the agent |
| **Native contract marker** | `/etc/horde/fabrica-native-agent-contract` contains `1`. Cloud-init refuses to configure an AMI without it. Write it only after the agent install is verified. |
| **Non-root service** | `horde-agent.service` is loaded and runs as a non-root `User=`. Cloud-init refuses root or an unset user. |

### What Fabrica's cloud-init does at boot

The same script runs for HTTP and TLS coordinators. It:

1. Checks the native contract marker, that `horde-agent.service` is loaded, and that its `User` is non-root
2. Writes `/etc/horde/fabrica-agent.env` (see [The Agent Environment File](#the-agent-environment-file))
3. Installs the systemd drop-in `/etc/systemd/system/horde-agent.service.d/fabrica.conf`, which loads that file with `EnvironmentFile=`, then enables and restarts `horde-agent.service`
4. Probes the coordinator's `/api/v1/server/ports` listener, retrying up to 30 times, 10 seconds apart
5. Writes the sentinel `/etc/horde/agent-configured` only after the probe passes and the service is active

HTTP mode starts the service before the probe, so the agent keeps reconnecting while the coordinator is slow. If the probe still fails after 30 attempts, cloud-init exits 1 and records the failure, and the service keeps running. TLS mode is fail-closed: it probes first, and if the probe fails after 30 attempts it writes nothing and exits 1.

`/etc/horde/agent-configured` means configuration and service start succeeded. It does not mean the agent is enrolled or approved.

**What the AMI must provide:**
- A systemd unit `horde-agent.service` for the agent binary, running as a non-root user
- The agent software itself — Fabrica does not install it
- The native contract marker (above)
- SSM agent running

---

## The Agent Environment File

Fabrica writes `/etc/horde/fabrica-agent.env` in systemd `EnvironmentFile` syntax:

```ini
Horde__Server=fabrica
Horde__ServerProfiles__fabrica__Name=fabrica
Horde__ServerProfiles__fabrica__Url=http://10.0.1.10:5000
```

The HTTP URL uses the coordinator's private IP, resolved at `fabrica horde agents create`
time via Cloud Control. The port is `horde.port` (default `5000`). In TLS mode the URL is
`https://<horde.tls.serverName>:<port>`, and a configured CA adds `SSL_CERT_FILE`.

Fabrica no longer writes `/etc/horde/coordinator.conf` or `HORDE_COORDINATOR_*`
variables. Custom INI or Docker-based agent AMIs must add the native contract
themselves before they can enroll.

---

## Build Methods

### Native Install

Install the Horde agent binary directly on the AMI. This is the cleanest approach
for a dedicated agent image — no Docker overhead, no compose stack, just the agent.

#### Step 1: Launch a bake instance

```bash
# Ubuntu 22.04, x86_64, same instance type as production (c7i.xlarge recommended)
aws ec2 run-instances \
  --image-id ami-0abcdef1234567890 \
  --instance-type c7i.xlarge \
  --count 1 \
  --tag-specifications 'ResourceType=instance,Tags=[{Key=Name,Value=fabrica-horde-agent-bake}]'
```

#### Step 2: Install the Horde agent

Install the .NET 10 ASP.NET Core runtime from Canonical's dotnet backports PPA
(`ppa:dotnet/backports`, signing key fingerprint `45A3F127159BE9E5017811C62125B164E8E5D3FA`).
The generated bake pins that fingerprint in a keyring instead of using
`add-apt-repository`:

```bash
sudo add-apt-repository -y ppa:dotnet/backports
sudo apt-get update -q
sudo apt-get install -y aspnetcore-runtime-10.0
```

Unzip the stock agent package (`HordeAgent.zip`, from your coordinator's
`/api/v1/agentsoftware/default/zip`) into `/opt/horde-agent`, root-owned:

```bash
sudo apt-get install -y unzip
sudo mkdir -p /opt/horde-agent
sudo unzip -q HordeAgent.zip -d /opt/horde-agent
sudo chown -R root:root /opt/horde-agent
sudo chmod -R go-w /opt/horde-agent
```

There is no native `horde-agent` executable. The agent runs as `dotnet HordeAgent.dll`.

#### Step 3: Create the systemd unit

Create a non-root service user (for example `horde-agent`) and give it write access to
the agent's data directories, described below. Then create
`/etc/systemd/system/horde-agent.service`:

```ini
[Unit]
Description=Unreal Horde Build Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=horde-agent
WorkingDirectory=/opt/horde-agent/Data
ExecStart=/usr/bin/dotnet /opt/horde-agent/HordeAgent.dll -WorkingDir=/opt/horde-agent/Data
Environment=DOTNET_NOLOGO=1
Environment=DOTNET_CLI_TELEMETRY_OPTOUT=1
Restart=always
RestartSec=10
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/opt/horde-agent/Data /var/lib/horde-agent/.local/share
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

Do not add `EnvironmentFile=` or coordinator flags to the unit. Cloud-init installs
the `Horde__…` settings in its own drop-in, and the stock agent reads them from the
environment.

**Writable paths.** The agent writes its server profile list (`servers.json`) and
other state under two locations. The service user must be able to write both:

- `/opt/horde-agent/Data`
- `$HOME/.local/share` for the service user

If the agent logs `Access to the path '/servers.json' is denied`, the service user
cannot write one of these locations. Fix the ownership (for example
`sudo chown -R horde-agent:horde-agent /opt/horde-agent/Data`) and confirm the
service user's `HOME` is the directory you expect.

#### Step 4: Enable the service

```bash
sudo systemctl daemon-reload
sudo systemctl enable horde-agent
```

Do **not** start the service during the bake — the instance should boot clean,
and Fabrica's cloud-init will start it with the correct coordinator IP.

#### Step 5: Verify the bake

```bash
# Confirm the agent and the .NET 10 runtime are present
test -f /opt/horde-agent/HordeAgent.dll
dotnet --list-runtimes | grep 'Microsoft.NETCore.App 10\.'

# Confirm the systemd unit is enabled
systemctl is-enabled horde-agent

# Confirm SSM agent is running
systemctl status amazon-ssm-agent

# Confirm the unit runs as a non-root user
systemctl show horde-agent --property=User --value

# Write the native contract marker only after the install above is verified
printf '1\n' | sudo tee /etc/horde/fabrica-native-agent-contract
```

#### Step 6: Create the AMI

```bash
AMI_NAME="fabrica-horde-agent-$(date +%Y%m%d)"
aws ec2 create-image \
  --instance-id <instance-id> \
  --name "$AMI_NAME" \
  --description "Dedicated Horde agent AMI — agent binary only, no coordinator stack" \
  --no-reboot \
  --tag-specifications \
    "ResourceType=image,Tags=[{Key=ManagedBy,Value=fabrica},{Key=FabricaModule,Value=horde},{Key=Name,Value=$AMI_NAME}]" \
    "ResourceType=snapshot,Tags=[{Key=ManagedBy,Value=fabrica},{Key=FabricaModule,Value=horde},{Key=Name,Value=$AMI_NAME}]"
```

The `snapshot` tag specification is required: without it the root snapshot is
created untagged and stays invisible to `ManagedBy`-based sweeps and teardown.

Note the resulting AMI ID.

---

### Packer Template (Optional)

For repeatable builds, a Packer HCL template can automate the bake. Example:

```hcl
source "amazon-ebs" "horde-agent" {
  ami_name          = "fabrica-horde-agent-${formatdate("YYYYMMDD", timestamp())}"
  instance_type     = "c7i.xlarge"
  region            = var.aws_region
  source_ami_filter {
    filters = {
      name                = "ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"
      root-device-type    = "ebs"
      virtualization-type = "hvm"
    }
    most_recent = true
    owners      = ["099720109477"]
  }
  tags = {
    Name        = "fabrica-horde-agent"
    ManagedBy   = "fabrica"
    Purpose     = "horde-agent"
  }
}

build {
  sources = ["source.amazon-ebs.horde-agent"]

  provisioner "shell" {
    inline = [
      "sudo apt-get update && sudo apt-get install -y unzip",
      # Install the .NET 10 runtime (see the generated bake for the pinned PPA) and unzip the agent
      "sudo add-apt-repository -y ppa:dotnet/backports && sudo apt-get update && sudo apt-get install -y aspnetcore-runtime-10.0",
      "sudo mkdir -p /opt/horde-agent",
      "sudo unzip -q /tmp/HordeAgent.zip -d /opt/horde-agent",
      "sudo chown -R root:root /opt/horde-agent",
      # Ensure SSM agent is running
      "sudo systemctl enable amazon-ssm-agent",
      "sudo systemctl start amazon-ssm-agent",
    ]
  }

  provisioner "file" {
    source      = "HordeAgent.zip"
    destination = "/tmp/HordeAgent.zip"
  }

  provisioner "file" {
    source      = "horde-agent.service"
    destination = "/tmp/horde-agent.service"
  }

  provisioner "shell" {
    inline = [
      "sudo cp /tmp/horde-agent.service /etc/systemd/system/",
      "sudo systemctl daemon-reload",
      "sudo systemctl enable horde-agent",
    ]
  }
}
```

---

## Known-Good AMIs

Only AMIs that have passed the verification checklist below are listed here.
If your account has no verified agent AMI yet, the table will show TBD — you
need to bake one.

| Region | AMI ID | Name / notes | Method | Verified |
|--------|--------|-------------|--------|----------|
| TBD | TBD | SSM overlay on the agent base — fail-closed SSM Agent; private-subnet SSM Online | Image Builder overlay | Yes (SSM PingStatus Online, no public IP) |
| TBD | TBD | Stub agent for road test, Ubuntu 22.04 | Native | superseded for private SSM; use overlay AMI above |

> **Note:** These AMI IDs are account- and region-specific. Bake your own AMI
> per the guide above for production use. AMIs are private (`--owners self`)
> and region-scoped — do not share IDs across accounts.

After a successful bake, record the AMI ID here and in `fabrica.yaml`. Keep this
table updated as you bake new versions. AMIs are private (`--owners self`) and
region-scoped — do not share IDs across accounts.

---

## Verification Checklist

**Do not use the AMI with Fabrica until these checks pass on a test instance.**

| # | Check | Command | Pass criteria |
|---|-------|---------|---------------|
| 1 | SSM agent running | `systemctl status amazon-ssm-agent` | `active (running)` |
| 2 | Agent and runtime present | `test -f /opt/horde-agent/HordeAgent.dll && dotnet --list-runtimes` | `HordeAgent.dll` exists and a `Microsoft.NETCore.App 10.x` runtime is listed |
| 3 | Systemd unit enabled | `systemctl is-enabled horde-agent` | `enabled` |
| 4 | Native contract marker | `cat /etc/horde/fabrica-native-agent-contract` | `1` |
| 5 | Agent starts | `sudo systemctl start horde-agent && systemctl status horde-agent` | `active (running)` or `exited 0` |
| 6 | No coordinator services | `systemctl list-units \| grep -E 'mongodb\|redis\|horde-server'` | No matches |
| 7 | SSM session works | `aws ssm start-session --target <instance-id>` | Connects successfully |

### Full verification script

```bash
#!/bin/bash
set -euo pipefail

echo "=== Horde Agent AMI Verification ==="

# 1. SSM agent
if systemctl is-active amazon-ssm-agent > /dev/null 2>&1; then
  echo "[PASS] SSM agent is running"
else
  echo "[FAIL] SSM agent is not running"
  exit 1
fi

# 2. Agent assembly and .NET 10 runtime
if [ -f /opt/horde-agent/HordeAgent.dll ] && dotnet --list-runtimes | grep -q 'Microsoft.NETCore.App 10\.'; then
  echo "[PASS] HordeAgent.dll present and the .NET 10 runtime is installed"
else
  echo "[FAIL] HordeAgent.dll or the .NET 10 runtime is missing"
  exit 1
fi

# 3. Systemd unit
if systemctl is-enabled horde-agent > /dev/null 2>&1; then
  echo "[PASS] horde-agent.service is enabled"
else
  echo "[FAIL] horde-agent.service is not enabled"
  exit 1
fi

# 4. Native contract marker
if [ "$(cat /etc/horde/fabrica-native-agent-contract 2>/dev/null)" = "1" ]; then
  echo "[PASS] native agent contract marker is 1"
else
  echo "[FAIL] /etc/horde/fabrica-native-agent-contract is missing or not 1"
  exit 1
fi

# 4b. Service runs as a non-root user
agent_user=$(systemctl show horde-agent --property=User --value)
if [ -n "$agent_user" ] && [ "$agent_user" != "root" ]; then
  echo "[PASS] horde-agent runs as $agent_user"
else
  echo "[FAIL] horde-agent must set a non-root User="
  exit 1
fi

# 5. No coordinator services (should not be present on agent AMI)
if systemctl list-units 2>/dev/null | grep -qE 'mongodb|redis|horde-server'; then
  echo "[WARN] Coordinator services detected — this should be an agent-only AMI"
else
  echo "[PASS] No coordinator services found (agent-only)"
fi

echo "=== Verification complete ==="
```

---

## Setting the AMI in Fabrica

Add the agent AMI to your `fabrica.yaml`:

```yaml
horde:
  amiId: ami-coordinator-123  # coordinator AMI (separate from agent)
  agents:
    amiId: ami-agent-456       # dedicated agent AMI
    instanceType: c7i.xlarge
    minSize: 0
    desiredCapacity: 1
    maxSize: 4
```

Or pass it as a flag:

```bash
fabrica horde agents create --ami-id ami-agent-456 --yes
```

**Important:** `horde.amiId` and `horde.agents.amiId` are independent. Fabrica
fails `agents create` with a clear error if `horde.agents.amiId` is not set —
it does **not** silently fall back to the coordinator AMI.

### Placement (VPC/subnet)

The agent pool launches in the **same VPC and subnet as the coordinator** —
Fabrica resolves the coordinator's live placement at create time (subnet from
the coordinator instance, VPC from the coordinator security group). Set both
`horde.agents.vpcId` and `horde.agents.subnetId` (see `fabrica.example.yaml`)
to place the pool explicitly; the two keys must be set together. When
neither is set and the coordinator's placement cannot be determined, the pool
falls back to the account default VPC and `agents create` logs a warning.

---

## Network Connectivity

Agent instances run in the configured subnet (`horde.agents.subnetId`, or the
default VPC). Use a **private subnet** with no inbound internet traffic for
production.
The agent security group:
- Allows **no inbound** from the internet
- Allows **outbound** to the coordinator security group on port 5000 (HTTP) and 5002 (gRPC)
- The coordinator SG is added as the source for agent → coordinator traffic

Agents connect to the coordinator via its **private IP** (resolved at `agents create`
time). No public IPs are assigned.

---

## Operator Access (SSM)

Agent instances are provisioned with an IAM role (`AmazonSSMManagedInstanceCore`)
that enables **AWS Systems Manager Session Manager** for operator shell access.
No public SSH is configured — the security group does not open port 22 to the
internet.

To connect to an agent instance:

```bash
# Get the instance ID from the ASG
aws autoscaling describe-auto-scaling-instances \
  --auto-scaling-group-name fabrica-horde-agents-asg \
  --query 'AutoScalingInstances[].InstanceId'

# Connect via SSM
aws ssm start-session --target <instance-id>
```

Once connected, verify the agent:

```bash
# Check agent environment
cat /etc/horde/fabrica-agent.env

# Check agent process
ps aux | grep horde-agent

# Check cloud-init log
cat /var/log/fabrica-horde-agent-init.log

# Check configuration sentinel (service configured; enrollment still needs approval in Horde)
ls -la /etc/horde/agent-configured
```

---

## Common Pitfalls

| Problem | Cause | Fix |
|---------|-------|-----|
| Agent doesn't enroll | Wrong coordinator URL in the environment file | Verify `Horde__ServerProfiles__fabrica__Url` in `/etc/horde/fabrica-agent.env` |
| Agent connected but not listed as working | Enrollment is pending approval | Run `fabrica horde agents approve` to list pending agents, then `--host <name>` or `--all` |
| cloud-init exits 1 with `failed the listener check` | Coordinator unreachable or untrusted | Check the coordinator service, the agent-to-coordinator SG rule, and the agent CA (TLS). HTTP mode keeps the service running and retries; TLS mode wrote no agent configuration |
| `Access to the path '/servers.json' is denied` | Service user cannot write the agent data directory | Make `/opt/horde-agent/Data` and the service user's `$HOME/.local/share` writable by the `User=` account |
| cloud-init exits 1 at the contract check | Missing `/etc/horde/fabrica-native-agent-contract` or root service user | Bake the marker and a non-root `User=` into `horde-agent.service` (see Requirements) |
| SSM connection fails | SSM agent not running in AMI | Install and enable `amazon-ssm-agent` in the AMI |
| ASG instances fail health check | Agent binary missing or crashing | Check `/var/log/syslog` on the instance via SSM |
| Agent can't reach coordinator | Security group or routing issue | Verify agent SG allows outbound to coordinator port; confirm VPC routing |
| Using coordinator AMI as agent AMI | Wrong AMI in `horde.agents.amiId` | Bake a dedicated agent AMI; the coordinator AMI runs unnecessary services (MongoDB, Redis, full server) on every builder |
| `HordeAgent.dll` missing, or `dotnet` not found | The zip was not unzipped into `/opt/horde-agent`, or the .NET 10 runtime is not installed | Check `test -f /opt/horde-agent/HordeAgent.dll` and `dotnet --list-runtimes` |
| `horde.agents.amiId` missing | Config not set and no `--ami-id` flag | Set `horde.agents.amiId` in `fabrica.yaml` or pass `--ami-id` — Fabrica will not fall back to the coordinator AMI |
| AMI in wrong region | AMI IDs are region-scoped | Re-copy the AMI to each region: `aws ec2 copy-image` |
| `x86_64` vs `arm64` mismatch | AMI architecture doesn't match instance type | Build the AMI on the same instance family you plan to run (c7i = x86_64) |
