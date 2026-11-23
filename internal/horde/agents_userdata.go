package horde

import (
	"encoding/base64"
	"fmt"
	"net"
	"strings"
)

// AgentUserDataConfig is the input shape for the Horde agent cloud-init script.
//
// With Connection.TLS the agent targets the recorded HTTPS endpoint. Otherwise
// it targets CoordinatorIP:CoordinatorPort over HTTP. Both modes configure the
// stock agent through the native environment contract.
type AgentUserDataConfig struct {
	Connection Connection
	CAPath     string
	// CoordinatorIP is the private IP of the Horde coordinator (HTTP mode only).
	CoordinatorIP string
	// CoordinatorPort is the HTTP port of the Horde coordinator, default 5000
	// (HTTP mode only).
	CoordinatorPort int
}

// agentProbeAttempts and agentProbeIntervalSeconds bound how long cloud-init
// waits for the coordinator listener: 30 attempts, 10 seconds apart.
const (
	agentProbeAttempts        = 30
	agentProbeIntervalSeconds = 10
)

// agentReadyTail runs after the service is configured and the coordinator has
// answered. The sentinel means configuration and service start succeeded, not
// that enrollment is approved.
const agentReadyTail = `systemctl is-active --quiet horde-agent.service
touch /etc/horde/agent-configured
echo "Agent service configured; verify enrollment and an active session in Horde."
`

// validate checks the HTTP-mode fields. Returns nil if valid.
func (cfg *AgentUserDataConfig) validate() error {
	if net.ParseIP(cfg.CoordinatorIP) == nil {
		return fmt.Errorf("CoordinatorIP must be the coordinator's private IP address (set by horde agents create), got %q", cfg.CoordinatorIP)
	}
	if cfg.CoordinatorPort <= 0 {
		return fmt.Errorf("CoordinatorPort must be positive")
	}
	return nil
}

// AgentUserDataRaw renders the agent cloud-init script without base64 encoding.
// Used in tests to inspect script content directly.
func AgentUserDataRaw(cfg AgentUserDataConfig) (string, error) {
	c, err := agentConnection(cfg)
	if err != nil {
		return "", err
	}
	if err := ValidateTrustPath("horde.agents.caPath", cfg.CAPath); err != nil {
		return "", err
	}
	return agentScript(cfg, c), nil
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

// agentConnection returns the coordinator endpoint the agent is pointed at.
// TLS uses the recorded connection (DNS identity); HTTP uses the live address.
func agentConnection(cfg AgentUserDataConfig) (Connection, error) {
	if cfg.Connection.TLS {
		return cfg.Connection, cfg.Connection.validate()
	}
	if err := cfg.validate(); err != nil {
		return Connection{}, err
	}
	return Connection{Version: 1, Port: cfg.CoordinatorPort}, nil
}

// agentScript renders the native-contract script for both transports. The
// transport changes the URL, the curl protocol, and when the probe runs. TLS
// probes before writing configuration and fails closed. HTTP starts the service
// first so it can reconnect, then records success only after the probe passes.
func agentScript(cfg AgentUserDataConfig, c Connection) string {
	url := c.URL(cfg.CoordinatorIP)
	configure := agentConfigure(cfg, url)
	steps := configure + "probe_coordinator\n"
	if c.TLS {
		steps = "probe_coordinator\n" + configure
	}
	return agentPreamble(cfg, c, url) + agentProbeFunction(c) + steps + agentReadyTail
}

// agentPreamble verifies the AMI contract and the non-root service user, then
// sets the probe options. Nothing is written to the agent configuration here.
func agentPreamble(cfg AgentUserDataConfig, c Connection, url string) string {
	scheme := "http"
	if c.TLS {
		scheme = "https"
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
	script += "HORDE_URL=" + ShellQuote(url) + "\nCURL_OPTS=(--fail --silent --show-error --connect-timeout 5 --max-time 30 --proto '=" + scheme + "')\n"
	if cfg.CAPath != "" {
		script += "runuser -u \"$agent_user\" -- test -r " + ShellQuote(cfg.CAPath) + "\nCURL_OPTS+=(--cacert " + ShellQuote(cfg.CAPath) + ")\n"
	}
	return script
}

// agentProbeFunction defines probe_coordinator, a bounded retry of the listener
// check. Each attempt is one && chain because set -e does not apply inside a
// retry condition, so a failing command must fail the chain explicitly.
func agentProbeFunction(c Connection) string {
	return fmt.Sprintf(`probe_once() {
%[1]s
}
probe_coordinator() {
  local attempt
  for attempt in $(seq 1 %[2]d); do
    if probe_once; then
      return 0
    fi
    echo "Horde coordinator $HORDE_URL is not ready (attempt $attempt of %[2]d); retrying in %[3]ds" >&2
    sleep %[3]d
  done
  echo "Horde coordinator $HORDE_URL failed the listener check after %[2]d attempts. %[4]s" >&2
  return 1
}
`, probeChain(c), agentProbeAttempts, agentProbeIntervalSeconds, probeFailureHint(c))
}

// probeFailureHint says what the operator should do after the probe gives up.
// TLS fails before any configuration is written; HTTP has already configured
// and started the service, so the hint must not suggest it is unconfigured.
func probeFailureHint(c Connection) string {
	if c.TLS {
		return "Confirm the coordinator is running, the agent security group can reach it, and the agent CA (if set) trusts its certificate; then rerun cloud-init (agent configuration was not written)."
	}
	return "Confirm the coordinator is running and the agent security group can reach it. The agent service is already configured and started and keeps reconnecting, but /etc/horde/agent-configured was not written; rerun cloud-init once the coordinator is reachable."
}

// probeChain joins ListenerCheckScript's commands with && so a failure in any
// command fails the attempt.
func probeChain(c Connection) string {
	lines := strings.Split(strings.TrimSpace(ListenerCheckScript(c)), "\n")
	return strings.Join(lines, " &&\n")
}

// agentConfigure writes the stock EnvironmentFile and systemd drop-in, then
// (re)starts the unit. The rendered environment contains no secrets. The keys
// are pinned from the Horde 5.8.4 source: the config section is `Horde`, and
// ServerProfiles is a Dictionary<string, ServerProfile> keyed by profile name.
func agentConfigure(cfg AgentUserDataConfig, url string) string {
	env := "Horde__Server=fabrica\nHorde__ServerProfiles__fabrica__Name=fabrica\nHorde__ServerProfiles__fabrica__Url=" + url + "\n"
	if cfg.CAPath != "" {
		// systemd EnvironmentFile uses its own quoting, not shell expansion.
		env += "SSL_CERT_FILE=" + systemdQuote(cfg.CAPath) + "\n"
	}
	return "cat > /etc/horde/fabrica-agent.env <<'FABRICA_AGENT_ENV'\n" + env + "FABRICA_AGENT_ENV\n" +
		`cat > /etc/systemd/system/horde-agent.service.d/fabrica.conf <<'FABRICA_DROPIN'
[Service]
EnvironmentFile=/etc/horde/fabrica-agent.env
FABRICA_DROPIN
systemctl daemon-reload
systemctl enable horde-agent.service
systemctl restart horde-agent.service
`
}
