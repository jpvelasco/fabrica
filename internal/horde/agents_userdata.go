package horde

import (
	"fmt"
	"text/template"

	"github.com/jpvelasco/fabrica/internal/userdata"
)

// AgentUserDataConfig is the input shape for the Horde agent cloud-init script.
type AgentUserDataConfig struct {
	// CoordinatorIP is the private IP of the Horde coordinator.
	CoordinatorIP string
	// CoordinatorPort is the HTTP port of the Horde coordinator (default 5000).
	CoordinatorPort int
}

var agentUserDataRenderer = userdata.New(template.Must(template.New("horde-agent-userdata").Parse(`#!/bin/bash
set -euo pipefail
exec > >(tee /var/log/fabrica-horde-agent-init.log) 2>&1

echo "Configuring Horde agent to enroll against coordinator..."

# Set the coordinator address for the Horde agent
export HORDE_COORDINATOR_HOST={{ .CoordinatorIP }}
export HORDE_COORDINATOR_PORT={{ .CoordinatorPort }}

# Write the coordinator configuration for the agent
mkdir -p /etc/horde
cat > /etc/horde/coordinator.conf << EOF
[coordinator]
host = {{ .CoordinatorIP }}
port = {{ .CoordinatorPort }}
EOF

echo "Horde agent configured to connect to {{ .CoordinatorIP }}:{{ .CoordinatorPort }}"

# Start the Horde agent service
if command -v systemctl > /dev/null 2>&1; then
  systemctl enable horde-agent 2>/dev/null || true
  systemctl start horde-agent 2>/dev/null || true
fi

# If using Docker-based agent, start the container
if command -v docker > /dev/null 2>&1; then
  cd /etc/horde
  docker compose up -d agent 2>/dev/null || true
fi

echo "Horde agent initialization complete"
touch /var/lib/cloud/instance/horde-agent-ready
`)))

// validate checks required fields. Returns nil if valid.
func (cfg *AgentUserDataConfig) validate() error {
	if cfg.CoordinatorIP == "" {
		return fmt.Errorf("CoordinatorIP must not be empty")
	}
	if cfg.CoordinatorPort <= 0 {
		return fmt.Errorf("CoordinatorPort must be positive")
	}
	return nil
}

// AgentUserDataRaw renders the agent cloud-init script without base64 encoding.
// Used in tests to inspect script content directly.
func AgentUserDataRaw(cfg AgentUserDataConfig) (string, error) {
	if err := userdata.Prepare(nil, cfg.validate); err != nil {
		return "", err
	}
	return agentUserDataRenderer.Render(cfg)
}

// AgentUserData renders the agent cloud-init script and returns it
// base64-encoded (the format EC2 expects for UserData in Cloud Control).
func AgentUserData(cfg AgentUserDataConfig) (string, error) {
	if err := userdata.Prepare(nil, cfg.validate); err != nil {
		return "", err
	}
	return agentUserDataRenderer.RenderBase64(cfg)
}
