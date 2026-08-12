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
	if !strings.Contains(raw, "coordinator.conf") {
		t.Error("user data should write coordinator.conf")
	}
	if !strings.Contains(raw, "horde-agent-ready") {
		t.Error("user data should mark agent-ready")
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
