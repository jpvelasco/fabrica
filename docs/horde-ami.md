# Building a Horde AMI

`fabrica horde create` requires an AMI (`horde.amiId` in `fabrica.yaml`) that already contains
MongoDB, Redis, and the Horde server. Fabrica's cloud-init script only handles final configuration
and service startup — it does not install any software.

This document explains what the AMI must contain, how to build one, and common pitfalls.

---

## Requirements

The AMI must meet all of the following:

| Requirement | Detail |
|-------------|--------|
| **OS** | Ubuntu 22.04 LTS (jammy) — cloud-init script targets Ubuntu |
| **MongoDB** | Version 7.0, installed and enabled as `mongod` systemd unit |
| **Redis** | Version 6.2 or later, installed and enabled as `redis-server` (or `redis`) systemd unit |
| **Horde server** | Installed and enabled as `horde` systemd unit |
| **Horde config path** | The `horde` unit must read `/etc/horde/Server.json` as its config file |
| **Architecture** | `x86_64` (required for m7i instances) |

At boot, Fabrica's cloud-init script will:
1. Wait for `mongod` to become healthy
2. Create the `horde` MongoDB user with a generated password
3. Write `/etc/horde/Server.json` with the connection strings and ports
4. Restart `redis-server` and `horde` in dependency order

---

## Option 1: Docker Compose (recommended for most studios)

Epic ships an official Docker Compose configuration that bundles MongoDB, Redis, and the Horde
server together. Access requires a GitHub account linked to an EpicGames organization.

**Prerequisites:**
- GitHub account with EpicGames org access: https://www.unrealengine.com/en-US/ue-on-github
- GitHub Personal Access Token (classic) with `read:packages` scope
- Docker CE installed on the build machine

**Steps:**

1. Launch an Ubuntu 22.04 EC2 instance (same type you plan to use in production).

2. Install Docker CE:
   ```bash
   curl -fsSL https://get.docker.com | sh
   ```

3. Log in to GitHub Container Registry:
   ```bash
   echo "<YOUR_GITHUB_PAT>" | docker login ghcr.io -u <YOUR_GITHUB_USERNAME> --password-stdin
   ```

4. Find the official Docker Compose file in your Unreal Engine source checkout:
   ```
   Engine/Source/Programs/Horde/HordeServer/docker-compose.yml
   ```
   Copy it to the instance and customize credentials before starting.

5. Start the stack and verify all services are healthy:
   ```bash
   docker compose up -d
   docker compose ps
   ```

6. Create a `horde.service` systemd unit that wraps the Docker Compose stack, starts
   after `network-online.target`, and reads `/etc/horde/Server.json` as its config.

7. Stop all services, create an AMI from the instance via the AWS console or CLI:
   ```bash
   aws ec2 create-image \
     --instance-id <instance-id> \
     --name "fabrica-horde-$(date +%Y%m%d)" \
     --no-reboot
   ```

8. Note the resulting AMI ID (e.g. `ami-0abc123def456`) and add it to `fabrica.yaml`:
   ```yaml
   horde:
     amiId: ami-0abc123def456
   ```

---

## Option 2: Native install (no Docker)

If your studio cannot use Docker in production, install Horde natively using the .NET 8 runtime.

1. Install .NET 8 SDK:
   ```bash
   wget https://packages.microsoft.com/config/ubuntu/22.04/packages-microsoft-prod.deb -O packages-microsoft-prod.deb
   dpkg -i packages-microsoft-prod.deb
   apt-get update && apt-get install -y dotnet-sdk-8.0
   ```

2. Build the Horde server from your UE source checkout:
   ```bash
   cd Engine/Source/Programs/Horde
   dotnet publish HordeServer/HordeServer.csproj -c Release -o /opt/horde
   ```

3. Install MongoDB 7.0 and Redis from their official apt repositories.

4. Create `/etc/systemd/system/horde.service`:
   ```ini
   [Unit]
   Description=Horde Server
   After=mongod.service redis-server.service
   Requires=mongod.service

   [Service]
   ExecStart=/usr/bin/dotnet /opt/horde/HordeServer.dll
   WorkingDirectory=/opt/horde
   EnvironmentFile=/etc/horde/env
   Restart=on-failure

   [Install]
   WantedBy=multi-user.target
   ```

5. Create `/etc/horde/env` pointing at the config file:
   ```
   ASPNETCORE_ENVIRONMENT=Production
   Horde__DataDir=/etc/horde
   ```

   Fabrica writes `/etc/horde/Server.json` at boot; Horde reads it automatically from `DataDir`.

6. Enable the unit: `systemctl enable horde`

7. Stop all services and create the AMI as in Option 1 step 7.

---

## systemd Unit Naming

Fabrica's cloud-init restarts services with:
```bash
systemctl restart redis-server || systemctl restart redis
systemctl restart horde
```

**Ensure your AMI has a unit named exactly `horde`.** The Redis unit name varies by installation
(`redis-server` on Ubuntu apt, `redis` on some Docker setups) — both are tried.

---

## Verifying the AMI Before Using It

After building your AMI and launching a test instance from it:

```bash
# All three units should be enabled
systemctl is-enabled mongod redis-server horde

# MongoDB should be accepting connections
mongosh --eval "db.adminCommand('ping')"

# Horde config path must exist and be writable
ls -la /etc/horde/
```

Then run `fabrica horde create --dry-run` to verify Fabrica can build the plan before making
any AWS calls.

---

## Common Pitfalls

| Problem | Cause | Fix |
|---------|-------|-----|
| `mongod` not healthy at cloud-init time | Service enabled but takes >60s to start | Increase startup timeout or add `mongod` readiness checks to the AMI |
| `horde` unit not found | Unit named `horde-server` or `horde-coordinator` in AMI | Rename the unit or symlink it to `horde` |
| `/etc/horde/Server.json` not read | Horde binary reads config from a different path | Set `Horde__DataDir=/etc/horde` environment variable in the unit |
| `redis` service not found | Redis installed under a different unit name | Try both `redis-server` and `redis`; Fabrica tries both |
| AMI in wrong region | AMI IDs are region-scoped | Re-copy the AMI to each region you deploy to |
| `x86_64` vs `arm64` mismatch | AMI architecture doesn't match instance type | Build the AMI on the same instance family you plan to run |
