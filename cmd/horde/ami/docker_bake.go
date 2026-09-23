package ami

import "strings"

// Shared shell content for the Horde AMI bake. The Image Builder component
// (component.yaml.tmpl) and the Packer template (packer.hcl.tmpl) both embed
// these scripts so the two generated paths bake identical artifacts and
// cannot drift.

// hordeVersionPlaceholder is substituted with the configured Horde version in
// the bake scripts. "latest" is a valid version and tags
// fabrica-horde-server:latest.
const hordeVersionPlaceholder = "__HORDE_VERSION__"

// awsCliInstallScript installs the AWS CLI v2 and unzip. The stock jammy base
// ships neither: the ECR pull (docker install) and the S3 binary sync (native
// install) both need the CLI. The installer drops the binary into
// /usr/local/bin, which is not on the SSM session PATH — SSM payloads must
// call /usr/local/bin/aws by full path (see docs/horde-ami.md).
const awsCliInstallScript = `apt-get update -q
apt-get install -y unzip
curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-$(uname -m).zip" -o /tmp/awscliv2.zip
unzip -q /tmp/awscliv2.zip -d /tmp/awscli
/tmp/awscli/aws/install --update
rm -rf /tmp/awscli /tmp/awscliv2.zip`

// hordeDockerBakeScript writes the compose stack and configs under /etc/horde
// — the directory the horde systemd unit and the cloud-init script
// (internal/horde) start docker compose from — then pulls and tags the Horde
// server image from the account ECR repository using the instance role, and
// pre-pulls the dependency images.
const hordeDockerBakeScript = `set -euo pipefail

# Cloud-init and the horde systemd unit both start docker compose from
# /etc/horde, so the baked stack and configs live there.
mkdir -p /etc/horde

# The horde unit runs a one-shot docker compose up -d and never re-converges,
# so mongodb and redis must self-heal after a crash or OOM via their own
# restart policies.
cat >/etc/horde/docker-compose.yml <<'COMPOSE'
services:
  mongodb:
    image: mongo:7.0
    container_name: horde-mongodb
    volumes:
      - mongodb-data:/data/db
    command: mongod --noauth
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "mongosh", "--eval", "db.adminCommand('ping')"]
      interval: 10s
      timeout: 15s
      retries: 5
      start_period: 30s

  redis:
    image: redis:7.2
    container_name: horde-redis
    command: redis-server --save "" --appendonly no
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5

  horde:
    image: fabrica-horde-server:__HORDE_VERSION__
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
COMPOSE

cat >/etc/horde/globals.json <<'JSON'
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
JSON

cat >/etc/horde/server.json <<'JSON'
{
  "Horde": {
    "HttpPort": 5000,
    "Http2Port": 5002
  }
}
JSON

test -s /etc/horde/docker-compose.yml

# ghcr.io/epicgames/horde-server is private (the operator-side pull needs a
# GitHub token with read:packages scope), so the operator mirrors the image
# into ECR before baking. The bake pulls it with the instance role:
# ecr:GetAuthorizationToken requires Resource "*", the read actions can stay
# scoped to the repository ARN.
#
# The registry host and region are derived from the repository URL, so a
# nested ECR path (<acct>.dkr.ecr.<region>.amazonaws.com/team/horde-server)
# still resolves to the correct registry and a cross-region mirror still
# authenticates. ECR v2 hosts carry the region as the 4th dot segment
# (<acct>.dkr.ecr.<region>.amazonaws.com; FIPS: <acct>.dkr.ecr-fips.<region>).
# When the host does not name a region, the fallback chain is AWS_REGION /
# AWS_DEFAULT_REGION, then the AWS CLI profile's region (guarded so its
# non-zero exit cannot trip set -e); if none is set the bake fails with an
# explicit message instead of aborting silently.
ECR_REPO=REPLACE_WITH_ECR_REPOSITORY
ECR_REGISTRY="${ECR_REPO%%/*}"
ECR_REGION="$(printf "%s" "$ECR_REGISTRY" | cut -d. -f4)"
if [ -z "$ECR_REGION" ]; then
  ECR_REGION="${AWS_REGION:-${AWS_DEFAULT_REGION:-}}"
fi
if [ -z "$ECR_REGION" ]; then
  ECR_REGION="$(/usr/local/bin/aws configure get region 2>/dev/null || :)"
fi
if [ -z "$ECR_REGION" ]; then
  echo "could not derive an AWS region from ECR_REPO=$ECR_REPO; set AWS_REGION or use a full <acct>.dkr.ecr.<region>.amazonaws.com/<repo> URL" >&2
  exit 1
fi
/usr/local/bin/aws ecr get-login-password --region "$ECR_REGION" | docker login "$ECR_REGISTRY" -u AWS --password-stdin
docker pull "${ECR_REPO}:__HORDE_VERSION__"
docker tag "${ECR_REPO}:__HORDE_VERSION__" "fabrica-horde-server:__HORDE_VERSION__"
docker logout "$ECR_REGISTRY"
rm -f /root/.docker/config.json

# Bake the dependency images so the first boot needs no registry access.
docker pull mongo:7.0
docker pull redis:7.2`

// hordeDockerUnitText is the systemd unit for the docker-install Horde stack.
const hordeDockerUnitText = `[Unit]
Description=Horde Server (docker compose)
After=network-online.target docker.service
Requires=docker.service

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/etc/horde
ExecStart=/usr/bin/docker compose up -d
ExecStop=/usr/bin/docker compose down
ExecReload=/bin/sh -c '/usr/bin/docker compose pull && /usr/bin/docker compose up -d'

[Install]
WantedBy=multi-user.target`

// dockerBakeScript returns the docker-bake script for the given Horde version.
func dockerBakeScript(version string) string {
	return strings.ReplaceAll(hordeDockerBakeScript, hordeVersionPlaceholder, version)
}

// hordeDockerUnitCommand is the shell command that writes the horde unit
// file, for single-line HCL embedding in the Packer template.
func hordeDockerUnitCommand() string {
	return "cat >/etc/systemd/system/horde.service <<'UNIT'\n" + hordeDockerUnitText + "\nUNIT"
}

// hclInline renders a multi-line shell command as a single-line HCL
// double-quoted string (Packer inline list entries cannot span lines).
// Escapes for the HCL string literal: backslash, double quote, and newline.
// It also doubles the HCL interpolation opener ${ to $${ and the template
// directive opener %{ to %%{ so shell content like ${ECR_REPO%/*} survives
// Packer's HCL parse as a literal (the shell provisioner re-interprets it at
// run time). Bare $(…) command substitution is not HCL interpolation and is
// left untouched.
func hclInline(cmd string) string {
	escaped := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"${", `$${`,
		"%{", `%%{`,
		"\n", `\n`,
	).Replace(cmd)
	return `"` + escaped + `"`
}
