# Building a Horde AMI

`fabrica horde create` requires an AMI (`horde.amiId` in `fabrica.yaml`) that already
contains Docker CE and a Docker Compose stack for Horde. Fabrica's cloud-init script
only starts the stack — it does not install any software.

This document explains what the AMI must contain, how to build one, and common pitfalls.

---

## Requirements

The AMI must meet all of the following:

| Requirement | Detail |
|-------------|--------|
| **OS** | Ubuntu 22.04 LTS (jammy) — cloud-init script targets Ubuntu |
| **Docker CE** | Installed, enabled (`systemctl enable docker`), and running at boot |
| **Docker Compose** | `docker compose` (v2 plugin) available on PATH |
| **Compose stack** | `/etc/horde/docker-compose.yml` baked into the AMI |
| **Horde config** | `/etc/horde/globals.json` and `/etc/horde/server.json` baked into the AMI |
| **Architecture** | `x86_64` (required for m7i instances) |

At boot, Fabrica's cloud-init script will:
1. Wait for Docker to be running (`docker info`)
2. Run `cd /etc/horde && docker compose up -d`
3. HTTP-probe `http://localhost:5000/` until Horde responds
4. Touch the readiness sentinel (`/var/lib/cloud/instance/horde-ready`)

**Not required:** host `mongod` / `redis-server` / `horde` systemd units, host `mongosh`,
or a Fabrica-written `Server.json`. The compose stack manages all services and
configuration.

---

## The Compose Stack

The AMI must include a `docker-compose.yml` at `/etc/horde/docker-compose.yml` that
defines three services: MongoDB, Redis, and the Horde server. A minimal working
example:

```yaml
services:
  mongodb:
    image: mongo:7.0
    container_name: horde-mongodb
    volumes:
      - mongodb-data:/data/db
    command: mongod --noauth
    healthcheck:
      test: ["CMD", "mongosh", "--eval", "db.adminCommand('ping')"]
      interval: 10s
      timeout: 5s
      retries: 5

  redis:
    image: redis:7.2
    container_name: horde-redis
    command: redis-server --save "" --appendonly no
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5

  horde:
    image: fabrica-horde-server:latest
    container_name: horde-server
    ports:
      - "5000:5000"
      - "5002:5002"
    volumes:
      - /etc/horde/globals.json:/app/Defaults/globals.json:ro
      - /etc/horde/server.json:/app/Defaults/server.json:ro
    environment:
      - HORDE__REDISCONNECTIONSTRING=redis:6379
      - HORDE__MONGOCONNECTIONSTRING=mongodb://mongodb:27017/horde
      - ASPNETCORE_URLS=http://+:5000
      - HORDE__CONFIGPATH=/app/Defaults/globals.json
    depends_on:
      mongodb:
        condition: service_healthy
      redis:
        condition: service_healthy
    restart: unless-stopped

volumes:
  mongodb-data:
```

### Horde Config Files

Bake these into the AMI at `/etc/horde/`:

**`/etc/horde/globals.json`:**
```json
{
  "Version": 2,
  "horde": {
    "httpPort": 5000,
    "http2Port": 5002,
    "redisConnectionConfig": "redis:6379",
    "databaseConnectionString": "mongodb://mongodb:27017/horde"
  },
  "enabledPlugins": [
    "Compute",
    "Experimental",
    "Health"
  ]
}
```

**`/etc/horde/server.json`:**
```json
{
  "Horde": {
    "HttpPort": 5000,
    "Http2Port": 5002
  }
}
```

> **Note:** Do not include `UseLocalPerforceEnv` in `server.json` — the config
> remapping logic creates a `Horde:Plugins:Build` section that errors when the
> Build plugin is not loaded.

---

## Building the AMI

### Step 1: Launch a bake instance

```bash
# Ubuntu 22.04, x86_64, same instance type as production (m7i.2xlarge recommended)
aws ec2 run-instances \
  --image-id ami-0abcdef1234567890 \
  --instance-type m7i.2xlarge \
  --count 1 \
  --tag-specifications 'ResourceType=instance,Tags=[{Key=Name,Value=fabrica-horde-bake}]'
```

### Step 2: Install Docker CE

```bash
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker ubuntu
sudo systemctl enable docker
```

### Step 3: Build the Horde server image

If using Epic's official Docker image from GHCR:

```bash
# Log in to GitHub Container Registry
echo "<YOUR_GITHUB_PAT>" | docker login ghcr.io -u <YOUR_GITHUB_USERNAME> --password-stdin

# Pull the official image (or build from source)
docker pull ghcr.io/epicgames/unrealengine/horde-server:5.8.1
docker tag ghcr.io/epicgames/unrealengine/horde-server:5.8.1 fabrica-horde-server:latest
```

If building from source (UE 5.8.1+):

```bash
# The Dockerfile lives at Engine/Source/Programs/Horde/HordeServer/Dockerfile
docker build -f Engine/Source/Programs/Horde/HordeServer/Dockerfile \
  --build-arg UE_ENGINE_ROOT=/ue5 \
  -t fabrica-horde-server:latest .
```

### Step 4: Pre-pull dependency images

```bash
docker pull mongo:7.0
docker pull redis:7.2
```

### Step 5: Bake the compose stack and config into the AMI

```bash
sudo mkdir -p /etc/horde
sudo cp docker-compose.yml /etc/horde/
sudo cp globals.json /etc/horde/
sudo cp server.json /etc/horde/
```

### Step 6: Verify the stack works

```bash
sudo docker compose -f /etc/horde/docker-compose.yml up -d
sudo docker compose -f /etc/horde/docker-compose.yml ps
curl -sf http://localhost:5000/ && echo "Horde is healthy"
```

### Step 7: Create the AMI

```bash
# Stop the stack so the AMI is clean
sudo docker compose -f /etc/horde/docker-compose.yml down

aws ec2 create-image \
  --instance-id <instance-id> \
  --name "fabrica-horde-$(date +%Y%m%d)" \
  --description "Horde Docker compose AMI — MongoDB, Redis, Horde server" \
  --no-reboot
```

Note the resulting AMI ID and add it to `fabrica.yaml`:

```yaml
horde:
  amiId: ami-0abc123def456
  instanceType: m7i.2xlarge
  volumeSize: 100
```

---

## Verifying the AMI Before Using It

Launch a test instance from the new AMI and verify:

```bash
# Docker is running
docker info

# Compose stack starts cleanly
cd /etc/horde && docker compose up -d
docker compose ps

# Horde responds on port 5000
curl -sf http://localhost:5000/ && echo "OK"

# All three containers are healthy
docker inspect --format='{{.Name}}: {{.State.Health.Status}}' horde-mongodb horde-redis horde-server
```

Then run `fabrica horde create --dry-run` to verify Fabrica can build the plan
before making any AWS calls.

---

## Common Pitfalls

| Problem | Cause | Fix |
|---------|-------|-----|
| `Docker did not start within 60s` | Docker not enabled (`systemctl enable docker`) | Enable Docker before creating the AMI |
| `Horde did not become ready within 5m` | Compose file not at `/etc/horde/docker-compose.yml` | Verify path matches cloud-init expectation |
| Container exits immediately | Missing or malformed `globals.json` / `server.json` | Bake config files at `/etc/horde/` before creating AMI |
| `docker compose` not found | Docker Compose v2 plugin not installed | `apt install docker-compose-plugin` or use Docker CE install script |
| Images not found on first start | Dependency images not pre-pulled into the AMI | `docker pull mongo:7.0 redis:7.2` before `create-image` |
| AMI in wrong region | AMI IDs are region-scoped | Re-copy the AMI to each region: `aws ec2 copy-image` |
| `x86_64` vs `arm64` mismatch | AMI architecture doesn't match instance type | Build the AMI on the same instance family you plan to run |
| MongoDB auth errors | `globals.json` references auth but compose uses `--noauth` | Ensure `databaseConnectionString` does not include username/password |

---

## MongoDB Password Note

Fabrica generates a MongoDB password at `horde create` time and writes it to
`.fabrica/horde-credentials.yaml`. With the Docker compose AMI, this password is
**validated but not applied by cloud-init** — the compose stack manages MongoDB
credentials independently (typically `--noauth` for intra-container communication).

If your studio requires MongoDB authentication, configure it in the compose file
and `globals.json` baked into the AMI. The password in the credentials file is
kept for backward compatibility.