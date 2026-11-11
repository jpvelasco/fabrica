package horde

import (
	"encoding/base64"
	"fmt"
	"text/template"

	"github.com/jpvelasco/fabrica/internal/userdata"
)

// AgentUserDataConfig is the input shape for the Horde agent cloud-init script.
type AgentUserDataConfig struct {
	Connection Connection
	CAPath     string
	// CoordinatorIP is the private IP of the Horde coordinator.
	CoordinatorIP string
	// CoordinatorPort is the HTTP port of the Horde coordinator (default 5000).
	CoordinatorPort int
	// CoordinatorGRPCPort is the gRPC port of the Horde coordinator (default
	// 5002). Zero omits the gRPC lines from the rendered script.
	CoordinatorGRPCPort int
}

var agentUserDataRenderer = userdata.New(template.Must(template.New("horde-agent-userdata").Parse(`#!/bin/bash
set -euo pipefail
exec > >(tee /var/log/fabrica-horde-agent-init.log) 2>&1

echo "Configuring Horde agent to enroll against coordinator..."

# Set the coordinator address for the Horde agent
export HORDE_COORDINATOR_HOST={{ .CoordinatorIP }}
export HORDE_COORDINATOR_PORT={{ .CoordinatorPort }}
{{- if .CoordinatorGRPCPort }}
export HORDE_COORDINATOR_GRPC_PORT={{ .CoordinatorGRPCPort }}
{{- end }}

# Write the coordinator configuration for the agent
mkdir -p /etc/horde
cat > /etc/horde/coordinator.conf << EOF
[coordinator]
host = {{ .CoordinatorIP }}
port = {{ .CoordinatorPort }}
{{- if .CoordinatorGRPCPort }}
grpc_port = {{ .CoordinatorGRPCPort }}
{{- end }}
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
	if cfg.Connection.TLS {
		return tlsAgentUserData(cfg)
	}
	if err := userdata.Prepare(nil, cfg.validate); err != nil {
		return "", err
	}
	return agentUserDataRenderer.Render(cfg)
}

// AgentUserData renders the agent cloud-init script and returns it
// base64-encoded (the format EC2 expects for UserData in Cloud Control).
func AgentUserData(cfg AgentUserDataConfig) (string, error) {
	raw, err := AgentUserDataRaw(cfg)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString([]byte(raw)), nil
}

func tlsAgentUserData(cfg AgentUserDataConfig) (string, error) {
	c := cfg.Connection
	if err := c.validate(); err != nil {
		return "", err
	}
	if err := ValidateTrustPath("horde.agents.caPath", cfg.CAPath); err != nil {
		return "", err
	}
	script := `#!/bin/bash
set -euo pipefail
exec > >(tee /var/log/fabrica-horde-agent-init.log) 2>&1
test "$(cat /etc/horde/fabrica-native-agent-contract)" = 1
test "$(systemctl show horde-agent.service --property=LoadState --value)" = loaded
agent_user=$(systemctl show horde-agent.service --property=User --value)
test -n "$agent_user" && test "$agent_user" != root
test "$(id -u "$agent_user")" != 0
umask 077
mkdir -p /etc/systemd/system/horde-agent.service.d
`
	script += "HORDE_URL=" + ShellQuote(c.URL("")) + "\nCURL_OPTS=(--fail --silent --show-error --connect-timeout 5 --max-time 30 --proto '=https')\n"
	if cfg.CAPath != "" {
		script += "test -r " + ShellQuote(cfg.CAPath) + "\nCURL_OPTS+=(--cacert " + ShellQuote(cfg.CAPath) + ")\n"
	}
	script += ListenerCheckScript(c)
	// systemd EnvironmentFile uses its own quoted value syntax, not shell
	// expansion. Names/URLs are validated; quote remote paths separately.
	env := "Horde__Server=fabrica\nHorde__ServerProfiles__fabrica__Name=fabrica\nHorde__ServerProfiles__fabrica__Url=" + c.URL("") + "\n"
	if cfg.CAPath != "" {
		env += "SSL_CERT_FILE=" + systemdQuote(cfg.CAPath) + "\n"
	}
	script += "cat > /etc/horde/fabrica-agent.env <<'FABRICA_AGENT_ENV'\n" + env + "FABRICA_AGENT_ENV\n"
	script += `cat > /etc/systemd/system/horde-agent.service.d/fabrica.conf <<'FABRICA_DROPIN'
[Service]
EnvironmentFile=/etc/horde/fabrica-agent.env
FABRICA_DROPIN
systemctl daemon-reload
systemctl enable horde-agent.service
systemctl restart horde-agent.service
systemctl is-active --quiet horde-agent.service
touch /etc/horde/agent-configured
echo "Agent service configured; verify enrollment and an active session in Horde."
`
	return script, nil
}
