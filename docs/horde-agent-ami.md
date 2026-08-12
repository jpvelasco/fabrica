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
it does not install any software. The AMI must contain the agent binary (or container
image) pre-baked.

See [docs/horde-ami.md](horde-ami.md) for the **coordinator** AMI build guide.

---

## Requirements

The AMI must meet all of the following:

| Requirement | Detail |
|-------------|--------|
| **OS** | Ubuntu 22.04 LTS (jammy) — cloud-init script targets Ubuntu |
| **Horde agent** | The Unreal Horde agent binary installed and available on PATH, or a Docker image pre-loaded |
| **SSM Agent** | `amazon-ssm-agent` installed, enabled (`systemctl enable`), and running — required for Session Manager access |
| **Architecture** | `x86_64` (matches default `c7i.xlarge` instance type) |
| **No full server** | The AMI should NOT include MongoDB, Redis, or the Horde coordinator server binary — only the agent |

### What Fabrica's cloud-init does at boot

Fabrica writes a cloud-init script that:

1. Writes the coordinator configuration to `/etc/horde/coordinator.conf` (INI format)
2. Sets `HORDE_COORDINATOR_HOST` and `HORDE_COORDINATOR_PORT` environment variables
3. Attempts to start `horde-agent` via `systemctl start horde-agent` (native install)
4. Falls back to `docker compose up -d agent` if Docker is available (trial mode only — see below)
5. Touches the readiness sentinel `/var/lib/cloud/instance/horde-agent-ready`

**What the AMI must provide:**
- A systemd unit `horde-agent.service` (native) **or** a Docker image + compose file with an `agent` service (trial mode)
- The agent software itself — Fabrica does not install it
- SSM agent running

---

## The Coordinator Configuration File

Fabrica writes `/etc/horde/coordinator.conf` with the following INI format:

```ini
[coordinator]
host = <private-ip>
port = <port>
```

The private IP is resolved from the coordinator instance at `fabrica horde agents create`
time via Cloud Control. The port defaults to `5000` (configurable via `horde.port`).

Your agent software must read this file (or the `HORDE_COORDINATOR_HOST` /
`HORDE_COORDINATOR_PORT` environment variables) to discover the coordinator endpoint.

---

## Build Methods

### Method 1: Native Install (Recommended)

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

Copy the agent binary to `/opt/horde-agent/` and ensure it's executable:

```bash
sudo mkdir -p /opt/horde-agent
sudo cp horde-agent /opt/horde-agent/
sudo chmod +x /opt/horde-agent/horde-agent
sudo ln -sf /opt/horde-agent/horde-agent /usr/local/bin/horde-agent
```

If using Epic's official Docker image as a binary source:

```bash
# Log in to GitHub Container Registry (if required)
echo "<YOUR_GITHUB_PAT>" | docker login ghcr.io -u <YOUR_GITHUB_USERNAME> --password-stdin

# Extract the binary from the image
docker pull ghcr.io/epicgames/unrealengine/horde-agent:5.8.0
docker create --name horde-agent-extract ghcr.io/epicgames/unrealengine/horde-agent:5.8.0
docker cp horde-agent-extract:/path/to/horde-agent /opt/horde-agent/
docker rm horde-agent-extract
```

> **Note:** Verify the binary path inside the container first with
> `docker run --rm ghcr.io/epicgames/unrealengine/horde-agent:5.8.0 which horde-agent`
> or inspect the image layers. The exact path depends on the image build.

#### Step 3: Create the systemd unit

Create `/etc/systemd/system/horde-agent.service`:

```ini
[Unit]
Description=Unreal Horde Build Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/etc/horde/coordinator.conf
ExecStart=/opt/horde-agent/horde-agent --coordinator-host ${HORDE_COORDINATOR_HOST} --coordinator-port ${HORDE_COORDINATOR_PORT}
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

> **Note:** The `EnvironmentFile` directive reads key-value pairs. Fabrica writes
> `/etc/horde/coordinator.conf` in INI format, but systemd's `EnvironmentFile`
> expects `KEY=VALUE` lines. The cloud-init script writes the INI format for
> the agent binary to read directly. If your systemd unit needs `EnvironmentFile`,
> adjust the unit to parse the INI format or use `ExecStartPre` to extract values.
>
> **Alternative** — set environment variables directly in the unit:
> ```ini
> [Service]
> Environment="HORDE_COORDINATOR_HOST=PLACEHOLDER"
> Environment="HORDE_COORDINATOR_PORT=5000"
> ```
> Fabrica's cloud-init writes the actual values to `/etc/horde/coordinator.conf`;
> the agent binary should read from there. The systemd unit above is a template —
> adapt it to match your agent binary's actual command-line flags.

#### Step 4: Enable the service

```bash
sudo systemctl daemon-reload
sudo systemctl enable horde-agent
```

Do **not** start the service during the bake — the instance should boot clean,
and Fabrica's cloud-init will start it with the correct coordinator IP.

#### Step 5: Verify the bake

```bash
# Confirm the binary is available
which horde-agent

# Confirm the systemd unit is enabled
systemctl is-enabled horde-agent

# Confirm SSM agent is running
systemctl status amazon-ssm-agent

# Test the config file format (simulating what Fabrica writes)
sudo mkdir -p /etc/horde
sudo bash -c 'cat > /etc/horde/coordinator.conf << EOF
[coordinator]
host = 10.0.0.1
port = 5000
EOF'
cat /etc/horde/coordinator.conf
```

#### Step 6: Create the AMI

```bash
aws ec2 create-image \
  --instance-id <instance-id> \
  --name "fabrica-horde-agent-$(date +%Y%m%d)" \
  --description "Dedicated Horde agent AMI — agent binary only, no coordinator stack" \
  --no-reboot
```

Note the resulting AMI ID.

---

### Method 2: Docker Container (Trial Mode)

Bake a Docker image containing the Horde agent, then create an AMI with Docker CE
installed. This method is supported as **trial mode** — it works but adds Docker
overhead to every agent instance. Prefer Method 1 for production.

#### Step 1: Launch a bake instance (same as Method 1)

#### Step 2: Install Docker CE

```bash
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker ubuntu
sudo systemctl enable docker
```

#### Step 3: Load the agent Docker image

```bash
# Pull the agent image
docker pull ghcr.io/epicgames/unrealengine/horde-agent:5.8.0
docker tag ghcr.io/epicgames/unrealengine/horde-agent:5.8.0 horde-agent:latest
```

Or load from a saved image:
```bash
docker load < horde-agent-image.tar
```

#### Step 4: Create a compose file with an agent service

Create `/etc/horde/docker-compose.yml` with **only the agent service** (no MongoDB,
no Redis, no coordinator server):

```yaml
services:
  agent:
    image: horde-agent:latest
    container_name: horde-agent
    environment:
      - HORDE_COORDINATOR_HOST=${HORDE_COORDINATOR_HOST}
      - HORDE_COORDINATOR_PORT=${HORDE_COORDINATOR_PORT}
    restart: unless-stopped
```

#### Step 5: Create a systemd unit for the agent

Create `/etc/systemd/system/horde-agent.service`:

```ini
[Unit]
Description=Unreal Horde Build Agent (Docker)
After=docker.service
Requires=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/etc/horde
ExecStart=/usr/bin/docker compose up -d agent
ExecStop=/usr/bin/docker compose down agent

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable horde-agent
```

#### Step 6: Create the AMI (same as Method 1)

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
      # Install the Horde agent binary
      "sudo mkdir -p /opt/horde-agent",
      "sudo cp /tmp/horde-agent /opt/horde-agent/",
      "sudo chmod +x /opt/horde-agent/horde-agent",
      "sudo ln -sf /opt/horde-agent/horde-agent /usr/local/bin/horde-agent",
      # Ensure SSM agent is running
      "sudo systemctl enable amazon-ssm-agent",
      "sudo systemctl start amazon-ssm-agent",
    ]
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
| us-west-2 | ami-0eb4ac363d0115cd6 | fabrica-horde-agent-20260812 — stub agent for road test, Ubuntu 22.04, SSM | Native | Yes (road test) |

> **Note:** The AMI above is operator-account–specific (account `575108928122`).
> Bake your own AMI per the guide above for production use. AMIs are private
> (`--owners self`) and region-scoped — do not share IDs across accounts.

After a successful bake, record the AMI ID here and in `fabrica.yaml`. Keep this
table updated as you bake new versions. AMIs are private (`--owners self`) and
region-scoped — do not share IDs across accounts.

---

## Verification Checklist

**Do not use the AMI with Fabrica until these checks pass on a test instance.**

| # | Check | Command | Pass criteria |
|---|-------|---------|---------------|
| 1 | SSM agent running | `systemctl status amazon-ssm-agent` | `active (running)` |
| 2 | Agent binary available | `which horde-agent` | Returns a path |
| 3 | Systemd unit enabled | `systemctl is-enabled horde-agent` | `enabled` |
| 4 | Config file writable | `sudo bash -c 'echo test > /etc/horde/coordinator.conf && cat /etc/horde/coordinator.conf'` | Write succeeds |
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

# 2. Agent binary
if command -v horde-agent > /dev/null 2>&1; then
  echo "[PASS] horde-agent binary found at $(which horde-agent)"
else
  echo "[FAIL] horde-agent binary not found on PATH"
  exit 1
fi

# 3. Systemd unit
if systemctl is-enabled horde-agent > /dev/null 2>&1; then
  echo "[PASS] horde-agent.service is enabled"
else
  echo "[FAIL] horde-agent.service is not enabled"
  exit 1
fi

# 4. Config file
sudo mkdir -p /etc/horde
sudo bash -c 'cat > /etc/horde/coordinator.conf << EOF
[coordinator]
host = 10.0.0.1
port = 5000
EOF'
if [ -f /etc/horde/coordinator.conf ]; then
  echo "[PASS] /etc/horde/coordinator.conf is writable"
else
  echo "[FAIL] Cannot write /etc/horde/coordinator.conf"
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

---

## Network Connectivity

Agent instances run in **private subnets** with no inbound internet traffic.
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
# Check agent config
cat /etc/horde/coordinator.conf

# Check agent process
ps aux | grep horde-agent

# Check cloud-init log
cat /var/log/fabrica-horde-agent-init.log

# Check readiness sentinel
ls -la /var/lib/cloud/instance/horde-agent-ready
```

---

## Common Pitfalls

| Problem | Cause | Fix |
|---------|-------|-----|
| Agent doesn't enroll | Wrong coordinator IP in config | Verify `/etc/horde/coordinator.conf` has the correct private IP |
| SSM connection fails | SSM agent not running in AMI | Install and enable `amazon-ssm-agent` in the AMI |
| ASG instances fail health check | Agent binary missing or crashing | Check `/var/log/syslog` on the instance via SSM |
| Agent can't reach coordinator | Security group or routing issue | Verify agent SG allows outbound to coordinator port; confirm VPC routing |
| Using coordinator AMI as agent AMI | Wrong AMI in `horde.agents.amiId` | Bake a dedicated agent AMI; the coordinator AMI runs unnecessary services (MongoDB, Redis, full server) on every builder |
| `horde-agent` not found on PATH | Binary not in `/usr/local/bin` or not symlinked | Ensure the binary is accessible via `which horde-agent` |
| `docker compose up -d agent` fails | No compose file at `/etc/horde/docker-compose.yml` or no `agent` service defined | For Docker method, bake the compose file with an `agent` service; prefer native install for production |
| `horde.agents.amiId` missing | Config not set and no `--ami-id` flag | Set `horde.agents.amiId` in `fabrica.yaml` or pass `--ami-id` — Fabrica will not fall back to the coordinator AMI |
| AMI in wrong region | AMI IDs are region-scoped | Re-copy the AMI to each region: `aws ec2 copy-image` |
| `x86_64` vs `arm64` mismatch | AMI architecture doesn't match instance type | Build the AMI on the same instance family you plan to run (c7i = x86_64) |
