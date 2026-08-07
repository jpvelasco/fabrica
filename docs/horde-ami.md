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
| **Job API** | The Horde server image must expose the job-creation API (`GET /api/v1/jobs` must not return 404). Fabrica submits BuildGraph jobs to this endpoint and fails fast with a clear message if the route is missing. |

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
    "Build",
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
docker pull ghcr.io/epicgames/horde-server:5.8.0
docker tag ghcr.io/epicgames/horde-server:5.8.0 fabrica-horde-server:latest
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

Start the stack and run the bake-time verification checks from the
[Bake-time Verification](#bake-time-verification-mandatory) section below.
**Do not proceed to create the AMI if the jobs API check fails.**

```bash
sudo docker compose -f /etc/horde/docker-compose.yml up -d
sudo docker compose -f /etc/horde/docker-compose.yml ps
curl -sf http://localhost:5000/ && echo "Health: OK"
curl -s -o /dev/null -w "%{http_code}" http://localhost:5000/api/v1/jobs
```

### Step 7: Create the AMI

```bash
# Stop the stack so the AMI is clean
sudo docker compose -f /etc/horde/docker-compose.yml down

aws ec2 create-image \
  --instance-id <instance-id> \
  --name "fabrica-horde-$(date +%Y%m%d)" \
  --description "Horde Docker compose AMI — MongoDB, Redis, job-capable Horde server" \
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

## GHCR vs Source — Choosing the Horde Server Image

**Try GHCR first.** If you have a GitHub PAT with `read:packages` scope and can
pull from `ghcr.io/epicgames/unrealengine/horde-server`, use the official image.
**Immediately verify the jobs API** after pulling — some tags expose only the
health endpoint and lack the job-creation controllers.

```bash
# Pull and test before baking
docker pull ghcr.io/epicgames/unrealengine/horde-server:5.8.1

# Run a quick container to probe the jobs API
docker run --rm -d --name horde-test -p 5000:5000 \
  ghcr.io/epicgames/unrealengine/horde-server:5.8.1
sleep 10
curl -s -o /dev/null -w "%{http_code}" http://localhost:5000/api/v1/jobs
docker stop horde-test
```

- **`200` or `401`** — the image has the jobs API. Tag it and proceed to bake.
- **`404`** — this tag does not include job-creation controllers. **Do not use
  this image.** Build from source instead.

### Build from source (UE 5.8.1+)

When GHCR images lack the jobs API (or you cannot authenticate), build from the
Unreal Engine source. The Dockerfile at
`Engine/Source/Programs/Horde/HordeServer/Dockerfile` produces a server with the
full API surface including Build/job controllers.

```bash
docker build -f Engine/Source/Programs/Horde/HordeServer/Dockerfile \
  --build-arg UE_ENGINE_ROOT=/ue5 \
  -t fabrica-horde-server:latest .
```

Ensure the Build plugin is enabled in `globals.json` (`"enabledPlugins"` includes
`"Build"`) so the job-creation routes are registered at startup.

---

## Known-Good AMIs

Only AMIs that have passed the bake-time verification above (health + jobs API)
are listed here. If your account has no verified AMI yet, the table will show
TBD — you need to bake one.

| Region | AMI ID | Name / notes | Source | Jobs API verified |
|--------|--------|-------------|--------|-------------------|
| us-west-2 | ami-0764d44c38ef85362 | fabrica-horde-20260806 — UE 5.8.0 Horde, Docker compose, mongo:7.0, redis:7.2 | ghcr.io/epicgames/horde-server:5.8.0 | Yes (200) |

After a successful bake, record the AMI ID here and in `fabrica.yaml`. Keep this
table updated as you bake new versions. AMIs are private (`--owners self`) and
region-scoped — do not share IDs across accounts.

---

## Bake-time Verification (mandatory)

**Do not create the AMI image until these checks pass on the bake instance.**
A health-only Horde (responds on `:5000` but no jobs API) will cause
`fabrica horde submit` and `fabrica ci trigger` to fail with 404 errors. Use `examples/BuildGraph.sample.xml` as a reference for the expected BuildGraph XML shape — a BuildGraph without `<Agent>`/`<Node>` elements will fail fast with a clear error before reaching the Horde API.

### Pass criteria

| Check | Command | Acceptable codes |
|-------|---------|------------------|
| Horde web UI responds | `curl -sf http://localhost:5000/` | `200` |
| Jobs API exists | `curl -s -o /dev/null -w "%{http_code}" http://localhost:5000/api/v1/jobs` | `200` (empty list OK), `401` (auth challenge OK) |
| Containers healthy | `docker inspect --format='{{.Name}}: {{.State.Health.Status}}' horde-mongodb horde-redis horde-server` | All three `healthy` |

### Fail criteria

- `GET /api/v1/jobs` returns `404` with an empty body or a "route missing" response — **the Horde server image does not include the job-creation controllers. Do not bake this AMI.**
- `GET /` returns anything other than `200` — Horde is not running or misconfigured.

### Full verification script

```bash
# Start the stack
cd /etc/horde && docker compose up -d

# Wait for containers to be healthy
sleep 15
docker compose ps

# 1. Health check
curl -sf http://localhost:5000/ && echo "Health: OK" || echo "Health: FAIL"

# 2. Jobs API — this is the gate
JOBS_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:5000/api/v1/jobs)
if [ "$JOBS_CODE" = "200" ] || [ "$JOBS_CODE" = "401" ]; then
  echo "Jobs API: OK (HTTP $JOBS_CODE)"
else
  echo "Jobs API: FAIL (HTTP $JOBS_CODE) — this AMI cannot accept jobs. Do not bake."
  exit 1
fi

# 3. Container health
docker inspect --format='{{.Name}}: {{.State.Health.Status}}' horde-mongodb horde-redis horde-server

# 4. Optional — check for swagger/docs endpoint
curl -sf http://localhost:5000/swagger/index.html && echo "Swagger: available" || echo "Swagger: not available (OK)"
```

Then run `fabrica horde create --dry-run` to verify Fabrica can build the plan
before making any AWS calls.

---

## Network Connectivity (allowedCidr)

`fabrica horde create` opens ports 5000 (HTTP) and 5002 (gRPC) in the security
group from the CIDR specified by `horde.allowedCidr` in `fabrica.yaml`. The
default is `10.0.0.0/8` — which covers `10.x` VPCs but **does not cover the
AWS default VPC** (`172.31.0.0/16`).

If your CodeBuild project, workstations, or operator machines live in a
`172.x` or `192.168.x` VPC, those clients will be blocked by the security group
when trying to reach the Horde coordinator. The symptom is a connection timeout
from CodeBuild ENIs or a `dial tcp <private-ip>:5000: i/o timeout` error in
build logs.

**Fix:** set `horde.allowedCidr` to your VPC CIDR in `fabrica.yaml`:

```yaml
horde:
  amiId: ami-0abc123def456
  allowedCidr: 172.31.0.0/16   # match your VPC CIDR
```

You can also set it to a broader range that covers both your VPC and any
peered VPCs (e.g. `10.0.0.0/8` for a `10.x` VPC, or `172.16.0.0/12` for
`172.16–31.x` VPCs). **Do not use `0.0.0.0/0`** — ports 5000/5002 should not
be internet-exposed.

> **Note:** If `horde create` can resolve your default VPC (via the provider's
> VPC resolver), it will automatically use the resolved VPC CIDR as the default
> for `allowedCidr` instead of `10.0.0.0/8`. Explicit config always wins.

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
| `POST /api/v1/jobs` returns 404 | Horde server image built without the job-creation API (no jobs/graphs/agents controllers) | Use a Horde server image that includes the full API surface — verify `curl -sf http://localhost:5000/api/v1/jobs` returns 200 (not 404) before baking the AMI |

---

## MongoDB Password Note

Fabrica generates a MongoDB password at `horde create` time and writes it to
`.fabrica/horde-credentials.yaml`. With the Docker compose AMI, this password is
**validated but not applied by cloud-init** — the compose stack manages MongoDB
credentials independently (typically `--noauth` for intra-container communication).

If your studio requires MongoDB authentication, configure it in the compose file
and `globals.json` baked into the AMI. The password in the credentials file is
kept for backward compatibility.

---

## Operator Access (SSM)

Horde instances are provisioned with an IAM role (`AmazonSSMManagedInstanceCore`)
that enables **AWS Systems Manager Session Manager** for operator shell access.
No public SSH is configured — the security group does not open port 22 to the
internet.

To connect:

```bash
aws ssm start-session --target <instance-id>
```

The SSM agent is included in standard Ubuntu 22.04 AMIs. If using a custom AMI,
ensure the `amazon-ssm-agent` package is installed and enabled.