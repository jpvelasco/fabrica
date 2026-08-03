package horde

import (
	"fmt"
	"text/template"

	"github.com/jpvelasco/fabrica/internal/userdata"
)

// UserDataConfig is the input shape for the Horde cloud-init script.
type UserDataConfig struct {
	MongoPassword string
	Port          int
	GRPCPort      int
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

# Start the Docker compose stack (MongoDB + Redis + Horde)
cd /etc/horde
docker compose up -d

# Wait for Horde to become healthy
for i in $(seq 1 30); do
  curl -sf http://localhost:{{ .Port }}/ > /dev/null 2>&1 && break
  [ $i -eq 30 ] && echo "ERROR: Horde did not become ready within 5m" && exit 1
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
