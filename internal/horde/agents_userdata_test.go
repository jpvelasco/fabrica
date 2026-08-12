package horde

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestAgentUserDataRaw(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.1.10",
		CoordinatorPort: 5000,
	}
	raw, err := AgentUserDataRaw(cfg)
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}

	if !strings.Contains(raw, "10.0.1.10") {
		t.Error("user data should contain coordinator IP")
	}
	if !strings.Contains(raw, "5000") {
		t.Error("user data should contain coordinator port")
	}
}

func TestAgentUserDataBase64(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.1.10",
		CoordinatorPort: 5000,
	}
	encoded, err := AgentUserData(cfg)
	if err != nil {
		t.Fatalf("AgentUserData: %v", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}

	if !strings.Contains(string(decoded), "10.0.1.10") {
		t.Error("decoded user data should contain coordinator IP")
	}
}

func TestAgentUserDataMissingCoordinatorIP(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "",
		CoordinatorPort: 5000,
	}
	_, err := AgentUserDataRaw(cfg)
	if err == nil {
		t.Fatal("expected error when CoordinatorIP is empty")
	}
}

func TestAgentUserDataMissingCoordinatorPort(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.1.10",
		CoordinatorPort: 0,
	}
	_, err := AgentUserDataRaw(cfg)
	if err == nil {
		t.Fatal("expected error when CoordinatorPort is 0")
	}
}

func TestAgentUserDataBase64Error(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "",
		CoordinatorPort: 0,
	}
	_, err := AgentUserData(cfg)
	if err == nil {
		t.Fatal("expected error from AgentUserData when validation fails")
	}
}

func TestAgentUserDataRawMissingPort(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.1.10",
		CoordinatorPort: -1,
	}
	_, err := AgentUserDataRaw(cfg)
	if err == nil {
		t.Fatal("expected error when CoordinatorPort is negative")
	}
}

func TestAgentUserDataServiceName(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.1.10",
		CoordinatorPort: 5000,
	}
	raw, err := AgentUserDataRaw(cfg)
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}

	// Must reference the agent service name, not the full server.
	if !strings.Contains(raw, "horde-agent") {
		t.Error("user data should reference horde-agent service")
	}
	// Must start the agent service specifically.
	if !strings.Contains(raw, "systemctl start horde-agent") {
		t.Error("user data should start horde-agent via systemctl")
	}
}

func TestAgentUserDataConfigPath(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.2.50",
		CoordinatorPort: 5000,
	}
	raw, err := AgentUserDataRaw(cfg)
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}

	// Must write to the correct config path.
	if !strings.Contains(raw, "/etc/horde/coordinator.conf") {
		t.Error("user data should write to /etc/horde/coordinator.conf")
	}
	// Must use INI format with [coordinator] section.
	if !strings.Contains(raw, "[coordinator]") {
		t.Error("user data should write INI format with [coordinator] section")
	}
	// Must write host and port keys.
	if !strings.Contains(raw, "host = 10.0.2.50") {
		t.Error("user data should write host key with coordinator IP")
	}
	if !strings.Contains(raw, "port = 5000") {
		t.Error("user data should write port key")
	}
}

func TestAgentUserDataNoSecrets(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.1.10",
		CoordinatorPort: 5000,
	}
	raw, err := AgentUserDataRaw(cfg)
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}

	// No passwords, tokens, or secrets should appear in the UserData.
	secretWords := []string{"password", "secret", "token", "api_key", "credential"}
	for _, word := range secretWords {
		if strings.Contains(strings.ToLower(raw), word) {
			t.Errorf("user data should not contain %q", word)
		}
	}
}

func TestAgentUserDataEnvVars(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.3.100",
		CoordinatorPort: 5002,
	}
	raw, err := AgentUserDataRaw(cfg)
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}

	// Must set environment variables for coordinator discovery.
	if !strings.Contains(raw, "HORDE_COORDINATOR_HOST=10.0.3.100") {
		t.Error("user data should set HORDE_COORDINATOR_HOST env var")
	}
	if !strings.Contains(raw, "HORDE_COORDINATOR_PORT=5002") {
		t.Error("user data should set HORDE_COORDINATOR_PORT env var")
	}
}

func TestAgentUserDataNoFullComposeStack(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.1.10",
		CoordinatorPort: 5000,
	}
	raw, err := AgentUserDataRaw(cfg)
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}

	// Must NOT run docker compose up -d without specifying the agent service.
	// The Docker fallback should only start the agent service, not the full stack.
	if strings.Contains(raw, "docker compose up -d\n") {
		t.Error("user data should not run 'docker compose up -d' without specifying agent service")
	}
	// If docker compose is used, it must target the agent service only.
	if strings.Contains(raw, "docker compose") {
		if !strings.Contains(raw, "docker compose up -d agent") {
			t.Error("user data docker compose should only start the agent service")
		}
	}
}

func TestAgentUserDataReadinessSentinel(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.1.10",
		CoordinatorPort: 5000,
	}
	raw, err := AgentUserDataRaw(cfg)
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}

	// Must touch the agent-specific readiness sentinel.
	if !strings.Contains(raw, "/var/lib/cloud/instance/horde-agent-ready") {
		t.Error("user data should touch horde-agent-ready sentinel")
	}
}

func TestAgentUserDataCustomPort(t *testing.T) {
	cfg := AgentUserDataConfig{
		CoordinatorIP:   "10.0.1.10",
		CoordinatorPort: 8080,
	}
	raw, err := AgentUserDataRaw(cfg)
	if err != nil {
		t.Fatalf("AgentUserDataRaw: %v", err)
	}

	// Custom port should appear in config and env vars.
	if !strings.Contains(raw, "port = 8080") {
		t.Error("user data should write custom port to config")
	}
	if !strings.Contains(raw, "HORDE_COORDINATOR_PORT=8080") {
		t.Error("user data should set custom port in env var")
	}
}
