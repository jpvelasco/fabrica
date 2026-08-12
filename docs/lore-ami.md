# Building a Lore AMI

`fabrica lore create` requires a **dedicated Lore AMI** (`lore.amiId` in `fabrica.yaml`
or `--ami-id` flag) that contains the `loreserver` binary — not a generic Ubuntu
image. Fabrica's cloud-init only mounts the EBS store, writes local/S3 store config,
and starts the service. It does not install or build Lore.

**This is not the Horde AMI.** The Horde AMI runs MongoDB, Redis, and the Horde
coordinator server. The Lore AMI runs only the `loreserver` process. Do not
confuse the two.

Fabrica can generate Image Builder artifacts for this AMI:

```bash
fabrica lore ami build
```

See [docs/horde-ami.md](horde-ami.md) for the **Horde coordinator** AMI build guide.

---

## Requirements

The AMI must meet all of the following:

| Requirement | Detail |
|-------------|--------|
| **OS** | Ubuntu 22.04 LTS (jammy) — cloud-init script targets Ubuntu |
| **loreserver** | The Epic Lore server binary installed and available on PATH, or a `loreserver.service` systemd unit |
| **SSM Agent** | `amazon-ssm-agent` installed, enabled (`systemctl enable`), and running — required for Session Manager access |
| **Architecture** | `x86_64` (matches default `m7i.xlarge` instance type; Graviton needs special build flags) |
| **Config flag** | `loreserver --config <DIR>` must be supported (Fabrica uses `/etc/loreserver`) |
| **Ports** | Process listens on **41337** (TCP gRPC + UDP QUIC) and **41339** (HTTP health, `GET /health_check`) |

### What Fabrica's cloud-init does at boot

Fabrica writes a cloud-init script that:

1. Resolves the attached EBS data volume (Xen `/dev/sdf`, Nitro NVMe)
2. Formats and mounts the data volume at `/opt/loreserver/store`
3. Creates store subdirectories (`immutable`, `mutable`, `lock`)
4. Writes `/etc/loreserver/local.toml` with store paths (local or S3 mode)
5. Starts `loreserver` via systemd if available, else `loreserver --config /etc/loreserver`
6. Touches the readiness sentinel `/var/lib/cloud/instance/lore-ready`

**What the AMI must provide:**
- The `loreserver` binary on PATH **or** a `loreserver.service` systemd unit
- SSM agent running
- Nothing else — Fabrica handles config, storage, and startup

---

## Build Methods

### Method 1: Native Install (Recommended)

Install the `loreserver` binary directly on the AMI. This is the cleanest approach
for a dedicated Lore image — no Docker overhead, just the server binary.

#### Step 1: Launch a bake instance

```bash
# Ubuntu 22.04, x86_64, same instance type as production (m7i.xlarge recommended)
aws ec2 run-instances \
  --image-id ami-0abcdef1234567890 \
  --instance-type m7i.xlarge \
  --count 1 \
  --tag-specifications 'ResourceType=instance,Tags=[{Key=Name,Value=fabrica-lore-bake}]'
```

Connect via SSM:
```bash
aws ssm start-session --target <instance-id>
```

#### Step 2: Install the loreserver binary

Copy the `loreserver` binary to `/opt/loreserver/` and ensure it's executable:

```bash
sudo mkdir -p /opt/loreserver
sudo cp loreserver /opt/loreserver/
sudo chmod +x /opt/loreserver/loreserver
sudo ln -sf /opt/loreserver/loreserver /usr/local/bin/loreserver
```

If building from source (Epic Games Lore repository):

```bash
# Clone and build
git clone https://github.com/EpicGames/lore.git
cd lore
mkdir -p Build && cd Build
cmake .. -DCMAKE_BUILD_TYPE=Release
cmake --build . --config Release
# The binary will be in Build/Bin/ or similar — verify with:
find .. -name loreserver -type f -executable
```

If using Epic's official Docker image as a binary source:

```bash
# Log in to GitHub Container Registry (if required)
echo "<YOUR_GITHUB_PAT>" | docker login ghcr.io -u <YOUR_GITHUB_USERNAME> --password-stdin

# Extract the binary from the image
docker pull ghcr.io/epicgames/unrealengine/loreserver:5.5.0
docker create --name loreserver-extract ghcr.io/epicgames/unrealengine/loreserver:5.5.0
docker cp loreserver-extract:/path/to/loreserver /opt/loreserver/
docker rm loreserver-extract
```

> **Note:** Verify the binary path inside the container first with
> `docker run --rm ghcr.io/epicgames/unrealengine/loreserver:5.5.0 which loreserver`
> or inspect the image layers. The exact path depends on the image build.

#### Step 3: Create the systemd unit

Create `/etc/systemd/system/loreserver.service`:

```ini
[Unit]
Description=Epic Lore Version Control Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=/opt/loreserver/loreserver --config /etc/loreserver
Restart=always
RestartSec=10
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

The `--config /etc/loreserver` flag tells loreserver where to find the
`local.toml` store configuration that Fabrica writes at boot.

#### Step 4: Enable the service

```bash
sudo systemctl daemon-reload
sudo systemctl enable loreserver
```

Do **not** start the service during the bake — the instance should boot clean,
and Fabrica's cloud-init will start it with the correct store config.

#### Step 5: Verify the bake

```bash
# Confirm the binary is available
which loreserver

# Confirm the systemd unit is enabled
systemctl is-enabled loreserver

# Confirm SSM agent is running
systemctl status amazon-ssm-agent

# Test the config directory is writable (simulating what Fabrica writes)
sudo mkdir -p /etc/loreserver
sudo bash -c 'cat > /etc/loreserver/local.toml << EOF
[immutable_store]
mode = "local"
path = "/opt/loreserver/store/immutable"
EOF'
cat /etc/loreserver/local.toml
```

#### Step 6: Create the AMI

```bash
aws ec2 create-image \
  --instance-id <instance-id> \
  --name "fabrica-lore-$(date +%Y%m%d)" \
  --description "Lore server AMI — loreserver binary only, no Horde stack" \
  --no-reboot
```

Note the resulting AMI ID.

---

### Method 2: Docker Container (Alternative)

Bake a Docker image containing loreserver, then create an AMI with Docker CE
installed. This method adds Docker overhead but may be simpler if your studio
already manages Lore via containers.

#### Step 1: Launch a bake instance (same as Method 1)

#### Step 2: Install Docker CE

```bash
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker ubuntu
sudo systemctl enable docker
```

#### Step 3: Load the loreserver Docker image

```bash
# Pull the official image
docker pull ghcr.io/epicgames/unrealengine/loreserver:5.5.0
docker tag ghcr.io/epicgames/unrealengine/loreserver:5.5.0 loreserver:latest
```

Or load from a saved image:
```bash
docker load < loreserver-image.tar
```

#### Step 4: Create the systemd unit

Create `/etc/systemd/system/loreserver.service`:

```ini
[Unit]
Description=Epic Lore Server (Docker)
After=docker.service
Requires=docker.service

[Service]
Type=simple
ExecStartPre=/usr/bin/docker start -a loreserver || /usr/bin/docker run -d --name loreserver --restart unless-stopped -p 41337:41337 -p 41337:41337/udp -p 41339:41339 -v /opt/loreserver:/opt/loreserver -v /etc/loreserver:/etc/loreserver loreserver:latest --config /etc/loreserver
ExecStart=/bin/true
ExecStop=/usr/bin/docker stop loreserver

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable loreserver
```

#### Step 5: Create the AMI (same as Method 1)

---

### Packer Template (Optional)

For repeatable builds, a Packer HCL template can automate the bake:

```hcl
source "amazon-ebs" "lore" {
  ami_name          = "fabrica-lore-${formatdate("YYYYMMDD", timestamp())}"
  instance_type     = "m7i.xlarge"
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
    Name        = "fabrica-lore"
    ManagedBy   = "fabrica"
    Purpose     = "loreserver"
  }
}

build {
  sources = ["source.amazon-ebs.lore"]

  provisioner "shell" {
    inline = [
      "sudo apt-get update && sudo apt-get install -y unzip",
      # Install the loreserver binary
      "sudo mkdir -p /opt/loreserver",
      "sudo cp /tmp/loreserver /opt/loreserver/",
      "sudo chmod +x /opt/loreserver/loreserver",
      "sudo ln -sf /opt/loreserver/loreserver /usr/local/bin/loreserver",
      # Ensure SSM agent is running
      "sudo systemctl enable amazon-ssm-agent",
      "sudo systemctl start amazon-ssm-agent",
    ]
  }

  provisioner "file" {
    source      = "loreserver.service"
    destination = "/tmp/loreserver.service"
  }

  provisioner "shell" {
    inline = [
      "sudo cp /tmp/loreserver.service /etc/systemd/system/",
      "sudo systemctl daemon-reload",
      "sudo systemctl enable loreserver",
    ]
  }
}
```

---

## Using `fabrica lore ami build`

Fabrica can generate Image Builder artifacts for the Lore AMI:

```bash
# Generate with defaults (native install, us-east-1)
fabrica lore ami build

# Target a specific Lore version
fabrica lore ami build --lore-version 5.5.0

# Include a Packer template for air-gapped studios
fabrica lore ami build --include-packer --output-dir build/lore-ami

# Preview what would be generated without writing files
fabrica lore ami build --dry-run
```

This produces:
- `image-builder-recipe.json` — EC2 Image Builder recipe
- `component.yaml` — Image Builder component (installs loreserver)
- `build-guide.md` — step-by-step build instructions
- `packer.pkr.hcl` — Packer template (with `--include-packer`)

No AWS calls are made — all output is written locally.

---

## Known-Good AMIs

Only AMIs that have passed the verification checklist below are listed here.
If your account has no verified Lore AMI yet, the table will show TBD — you
need to bake one.

| Region | AMI ID | Name / notes | Method | Verified |
|--------|--------|-------------|--------|----------|
| us-west-2 | TBD | Bake your own | Native | No |

> **Note:** AMIs are private (`--owners self`) and region-scoped — do not share
> IDs across accounts. After a successful bake, record the AMI ID here and in
> `fabrica.yaml`.

---

## Verification Checklist

**Do not use the AMI with Fabrica until these checks pass on a test instance.**

| # | Check | Command | Pass criteria |
|---|-------|---------|---------------|
| 1 | SSM agent running | `systemctl status amazon-ssm-agent` | `active (running)` |
| 2 | loreserver binary available | `which loreserver` | Returns a path |
| 3 | Systemd unit enabled | `systemctl is-enabled loreserver` | `enabled` |
| 4 | Config dir writable | `sudo bash -c 'echo test > /etc/loreserver/local.toml'` | Write succeeds |
| 5 | loreserver starts | `sudo systemctl start loreserver && systemctl status loreserver` | `active (running)` |
| 6 | Health endpoint responds | `curl -s http://localhost:41339/health_check` | Returns 200 |
| 7 | No Horde services | `systemctl list-units \| grep -E 'mongodb\|redis\|horde'` | No matches |

### Full verification script

```bash
#!/bin/bash
set -euo pipefail

echo "=== Lore AMI Verification ==="

# 1. SSM agent
if systemctl is-active amazon-ssm-agent > /dev/null 2>&1; then
  echo "[PASS] SSM agent is running"
else
  echo "[FAIL] SSM agent is not running"
  exit 1
fi

# 2. loreserver binary
if command -v loreserver > /dev/null 2>&1; then
  echo "[PASS] loreserver binary found at $(which loreserver)"
else
  echo "[FAIL] loreserver binary not found on PATH"
  exit 1
fi

# 3. Systemd unit
if systemctl is-enabled loreserver > /dev/null 2>&1; then
  echo "[PASS] loreserver.service is enabled"
else
  echo "[FAIL] loreserver.service is not enabled"
  exit 1
fi

# 4. Config directory
sudo mkdir -p /etc/loreserver
if sudo bash -c 'echo test > /etc/loreserver/local.toml' 2>/dev/null; then
  echo "[PASS] /etc/loreserver/ is writable"
else
  echo "[FAIL] Cannot write to /etc/loreserver/"
  exit 1
fi

# 5. No Horde services (should not be present on Lore AMI)
if systemctl list-units 2>/dev/null | grep -qE 'mongodb|redis|horde'; then
  echo "[WARN] Horde services detected — this should be a Lore-only AMI"
else
  echo "[PASS] No Horde services found (Lore-only)"
fi

echo "=== Verification complete ==="
```

---

## Setting the AMI in Fabrica

Add the Lore AMI to your `fabrica.yaml`:

```yaml
lore:
  amiId: ami-lore-123
  instanceType: m7i.xlarge
  volumeSize: 500
```

Or pass it as a flag:

```bash
fabrica lore create --ami-id ami-lore-123 --yes
```

---

## Network Connectivity

Lore instances run in **private subnets** with restricted inbound traffic.
The Lore security group:
- Allows inbound on ports **41337** (gRPC/QUIC) and **41339** (HTTP health) from `lore.allowedCidr` only
- Default `allowedCidr` is the VPC CIDR — not `0.0.0.0/0`
- Outbound is unrestricted for S3 access (when S3 store is enabled)

Lore clients (workstations, Horde agents) connect via the instance's **private IP**.
No public IPs are assigned.

---

## Operator Access (SSM)

Lore instances are provisioned with an IAM role (`AmazonSSMManagedInstanceCore`)
that enables **AWS Systems Manager Session Manager** for operator shell access.
No public SSH is configured — the security group does not open port 22 to the
internet.

To connect to the Lore instance:

```bash
# Get the instance ID from Fabrica status
fabrica lore status

# Connect via SSM
aws ssm start-session --target <instance-id>
```

Once connected, verify the server:

```bash
# Check store config
cat /etc/loreserver/local.toml

# Check loreserver process
ps aux | grep loreserver

# Check cloud-init log
cat /var/log/fabrica-lore-init.log

# Check readiness sentinel
ls -la /var/lib/cloud/instance/lore-ready

# Health check
curl -s http://localhost:41339/health_check
```

---

## Common Pitfalls

| Problem | Cause | Fix |
|---------|-------|-----|
| `loreserver` not found | Binary not on PATH or not symlinked | Ensure `which loreserver` returns a path |
| Health check fails (41339) | loreserver not running or wrong ports | Check `systemctl status loreserver` and cloud-init log |
| SSM connection fails | SSM agent not running in AMI | Install and enable `amazon-ssm-agent` in the AMI |
| Store config not found | `/etc/loreserver/local.toml` missing | Cloud-init writes this at boot — check `/var/log/fabrica-lore-init.log` |
| Data volume not mounted | EBS attach failed or device detection issue | Check cloud-init log for `resolve_data_dev` errors |
| S3 store authentication fails | IAM instance profile missing or wrong permissions | Verify `lore.storeBackend: "s3"` triggered profile creation; check IAM role policy |
| Using Horde AMI as Lore AMI | Wrong AMI in `lore.amiId` | Bake a dedicated Lore AMI; the Horde AMI runs unnecessary services (MongoDB, Redis, full server) |
| `lore.amiId` missing | Config not set and no `--ami-id` flag | Set `lore.amiId` in `fabrica.yaml` or pass `--ami-id` |
| AMI in wrong region | AMI IDs are region-scoped | Re-copy the AMI to each region: `aws ec2 copy-image` |
| `x86_64` vs `arm64` mismatch | AMI architecture doesn't match instance type | Build the AMI on the same instance family you plan to run (m7i = x86_64) |
| `loreserver` crashes on start | Missing config directory or wrong `--config` flag | Ensure systemd unit uses `--config /etc/loreserver` matching what Fabrica expects |
