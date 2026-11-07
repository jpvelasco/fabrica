package horde

import (
	"fmt"
	"text/template"

	"github.com/jpvelasco/fabrica/internal/userdata"
)

// UserDataConfig is the input shape for the Horde cloud-init script.
// MongoPassword is validated (non-empty) but never rendered: the MongoDB and
// Horde Admin passwords are generated on the instance at first boot, so no
// secret travels through user data. Port is used for the health check.
type UserDataConfig struct {
	MongoPassword string
	Port          int
}

var userDataRenderer = userdata.New(template.Must(template.New("horde-userdata").Parse(`#!/bin/bash
set -euo pipefail
exec > >(tee /var/log/fabrica-horde-init.log) 2>&1

echo "Starting Horde Docker stack..."

# Ensure Docker is running
for i in $(seq 1 12); do
  docker info > /dev/null 2>&1 && break
  [ $i -eq 12 ] && echo "ERROR: Docker did not start within 60s" && exit 1
  sleep 5
done

cd /etc/horde

# Secrets are generated here, on the instance, and never leave it. The compose
# stack reads them from /etc/horde/.env. Horde starts bound to loopback only
# (HORDE_BIND_ADDRESS=127.0.0.1) so nothing on the network can reach it before
# its Admin account exists. Read the Admin password over SSM from
# /etc/horde/admin-password.
umask 077
if [ ! -s /etc/horde/.env ]; then
  IMDS_TOKEN=$(curl -sf -X PUT http://169.254.169.254/latest/api/token -H 'X-aws-ec2-metadata-token-ttl-seconds: 60')
  PRIVATE_IP=$(curl -sf -H "X-aws-ec2-metadata-token: $IMDS_TOKEN" http://169.254.169.254/latest/meta-data/local-ipv4)
  openssl rand -hex 24 > /etc/horde/admin-password
  cat > /etc/horde/.env <<ENV
MONGO_PASSWORD=$(openssl rand -hex 24)
HORDE_BIND_ADDRESS=127.0.0.1
HORDE_SERVER_URL=http://${PRIVATE_IP}:{{ .Port }}
ENV
fi

# horde_up reports whether Horde answers HTTP at all. With built-in accounts
# the root URL redirects to the login page (or returns 401), which is healthy.
horde_up() {
  local code
  code=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:{{ .Port }}/ || true)
  [ "$code" != "000" ] && [ "${code:0:1}" != "5" ]
}

# Start the Docker compose stack (MongoDB + Redis + Horde).
# On cold boot the first compose up can exit non-zero: mongo's mongosh
# healthcheck (timeout: 5s) fails for the first ~30s, so the service_healthy
# dependency fails before the stack converges. Retry instead of aborting —
# the stack converges to all-healthy within ~90s.
compose_up() {
  local i
  for i in $(seq 1 8); do
    docker compose up -d && break
    [ $i -eq 8 ] && echo "ERROR: docker compose up -d failed after 8 attempts (~3m)" && exit 1
    sleep 20
  done
}
compose_up

# Wait for Horde to become healthy
for i in $(seq 1 30); do
  horde_up && break
  [ $i -eq 30 ] && echo "ERROR: Horde did not become ready within 5m" && exit 1
  sleep 10
done

# In built-in account mode the anonymous admin-create endpoint is first-caller-
# wins, so create the Admin account now, while the ports are loopback-only.
# 403 means the account already exists (a re-run).
ADMIN_PASSWORD=$(cat /etc/horde/admin-password)
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' \
  -d "{\"password\":\"${ADMIN_PASSWORD}\"}" \
  http://127.0.0.1:{{ .Port }}/api/v1/accounts/admin/create || true)
case "$code" in
  200|403) ;;
  *) echo "ERROR: Horde Admin account creation failed (HTTP $code)"; exit 1 ;;
esac
unset ADMIN_PASSWORD

# Publish Horde on the network (the security group still limits the source CIDR).
sed -i 's/^HORDE_BIND_ADDRESS=.*/HORDE_BIND_ADDRESS=0.0.0.0/' /etc/horde/.env
compose_up
for i in $(seq 1 30); do
  horde_up && break
  [ $i -eq 30 ] && echo "ERROR: Horde did not become ready after publishing within 5m" && exit 1
  sleep 10
done

echo "Horde stack is ready on port {{ .Port }}"
touch /var/lib/cloud/instance/horde-ready
`)))

// validate checks required fields. Returns nil if valid.
func (cfg *UserDataConfig) validate() error {
	if cfg.MongoPassword == "" {
		return fmt.Errorf("MongoPassword must not be empty")
	}
	return nil
}

// GenerateRaw renders the cloud-init script without base64 encoding.
// Used in tests to inspect script content directly.
func GenerateRaw(cfg UserDataConfig) (string, error) {
	if err := userdata.Prepare(nil, cfg.validate); err != nil {
		return "", err
	}
	return userDataRenderer.Render(cfg)
}

// Generate renders the cloud-init script and returns it base64-encoded
// (the format EC2 expects for UserData in Cloud Control).
func Generate(cfg UserDataConfig) (string, error) {
	if err := userdata.Prepare(nil, cfg.validate); err != nil {
		return "", err
	}
	return userDataRenderer.RenderBase64(cfg)
}
