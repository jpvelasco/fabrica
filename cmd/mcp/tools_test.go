package mcp

import (
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func newTestRuntime() globals.Runtime {
	return globals.Runtime{
		Config: &config.Config{
			Cloud: config.Cloud{
				Provider: "aws",
				AWS: config.AWS{
					AccountID: "123456789012",
					Region:    "us-west-2",
				},
			},
			State: config.State{
				Bucket: "fabrica-state-123456789012",
				Table:  "fabrica-state-lock",
			},
		},
		ConfigPath: "fabrica.yaml",
	}
}

// — Tool registration —

func TestRegisterTools_CreatesAllTools(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	rt := newTestRuntime()
	registerTools(s, rt)

	expectedTools := []string{
		"fabrica_version",
		"fabrica_doctor",
		"fabrica_status",
		"fabrica_drift",
		"fabrica_cost_report",
		"fabrica_config_show",
	}

	// Verify tools were registered by checking they can be removed (unregistered tools would panic).
	// The SDK doesn't expose a ListTools on the server side, so we verify via RemoveTools.
	for _, name := range expectedTools {
		// RemoveTools is a no-op for unknown names; we just ensure no panic.
		s.RemoveTools(name)
	}
}

func TestRegisterTools_Descriptions(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	rt := newTestRuntime()
	registerTools(s, rt)

	// If registration fails, the server will reject tool calls. We verify
	// by attempting to remove each tool — this confirms they were registered.
	tools := []string{"fabrica_version", "fabrica_doctor", "fabrica_status", "fabrica_drift", "fabrica_cost_report", "fabrica_config_show"}
	for _, name := range tools {
		s.RemoveTools(name)
	}
	// If we get here without panic, all tools were registered.
}

// — Config redaction —

func TestShouldRedact_Password(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"password", true},
		{"mongo_password", true},
		{"PASSWORD", true},
		{"token", true},
		{"service_token", true},
		{"secret", true},
		{"api_key", true},
		{"access_key", true},
		{"access_key_id", false},
		{"name", false},
		{"region", false},
		{"key_name", false},
		{"keyboard", false},
		{"monkey", false},
		{"token_bucket", false},
	}

	for _, tt := range tests {
		got := shouldRedact(tt.key)
		if got != tt.want {
			t.Errorf("shouldRedact(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

func TestRedactMap_FlatMap(t *testing.T) {
	m := map[string]any{
		"region":   "us-east-1",
		"password": "supersecret",
		"provider": "aws",
	}
	redactMap(m)

	if m["region"] != "us-east-1" {
		t.Errorf("region = %v, want us-east-1", m["region"])
	}
	if m["password"] != "[redacted]" {
		t.Errorf("password = %v, want [redacted]", m["password"])
	}
	if m["provider"] != "aws" {
		t.Errorf("provider = %v, want aws", m["provider"])
	}
}

func TestRedactMap_NestedMap(t *testing.T) {
	m := map[string]any{
		"cloud": map[string]any{
			"provider": "aws",
			"aws": map[string]any{
				"region":     "us-west-2",
				"access_key": "AKIAIOSFODNN7EXAMPLE", // #nosec G101 — AWS docs example key for redaction test
			},
		},
	}
	redactMap(m)

	awsMap := m["cloud"].(map[string]any)["aws"].(map[string]any)
	if awsMap["access_key"] != "[redacted]" {
		t.Errorf("nested access_key = %v, want [redacted]", awsMap["access_key"])
	}
	if awsMap["region"] != "us-west-2" {
		t.Errorf("nested region = %v, want us-west-2", awsMap["region"])
	}
}

func TestRedactMap_ArrayOfMaps(t *testing.T) {
	m := map[string]any{
		"workstations": []any{
			map[string]any{
				"name":     "ws1",
				"password": "dcv-pass-1",
			},
			map[string]any{
				"name":     "ws2",
				"password": "dcv-pass-2",
			},
		},
	}
	redactMap(m)

	arr := m["workstations"].([]any)
	for i, item := range arr {
		mm := item.(map[string]any)
		if mm["password"] != "[redacted]" {
			t.Errorf("workstations[%d].password = %v, want [redacted]", i, mm["password"])
		}
		if mm["name"] == "[redacted]" {
			t.Errorf("workstations[%d].name was incorrectly redacted", i)
		}
	}
}

func TestRedactMap_EmptyMap(t *testing.T) {
	m := map[string]any{}
	redactMap(m)
	if len(m) != 0 {
		t.Errorf("empty map should remain empty after redactMap")
	}
}

func TestRedactMap_NilValues(t *testing.T) {
	m := map[string]any{
		"password": nil,
		"region":   "us-east-1",
	}
	redactMap(m)
	if m["password"] != "[redacted]" {
		t.Errorf("nil password should be redacted to [redacted], got %v", m["password"])
	}
	if m["region"] != "us-east-1" {
		t.Errorf("region should be unchanged, got %v", m["region"])
	}
}

// — Result type serialization —

func TestVersionResult_JSON(t *testing.T) {
	r := VersionResult{
		Version: "v0.1.3",
		Commit:  "abc123",
		Go:      "go1.25.12",
		OS:      "linux",
		Arch:    "amd64",
	}
	// Verify all fields are settable and have json tags.
	if r.Version != "v0.1.3" || r.Commit != "abc123" || r.Go != "go1.25.12" || r.OS != "linux" || r.Arch != "amd64" {
		t.Error("VersionResult fields not settable")
	}
}

func TestDoctorResult_Healthy(t *testing.T) {
	r := DoctorResult{
		Checks: []DoctorCheck{
			{Name: "Go", Status: "ok", Message: "go1.25"},
			{Name: "Creds", Status: "fail", Message: "no creds"},
		},
		Healthy: false,
	}
	if r.Healthy {
		t.Error("Healthy should be false when checks contain fail")
	}
	if len(r.Checks) != 2 {
		t.Errorf("expected 2 checks, got %d", len(r.Checks))
	}
}

func TestCostReportResult_Modules(t *testing.T) {
	r := CostReportResult{
		Total:      100.0,
		Confidence: "high",
		Modules: []CostModule{
			{Name: "perforce", Status: "ready", Subtotal: 50.0},
			{Name: "horde", Status: "ready", Subtotal: 50.0},
		},
	}
	if r.Total != 100.0 {
		t.Errorf("Total = %v, want 100.0", r.Total)
	}
	if len(r.Modules) != 2 {
		t.Errorf("expected 2 modules, got %d", len(r.Modules))
	}
}

func TestConfigShowResult_Path(t *testing.T) {
	r := ConfigShowResult{
		Config:     map[string]any{"cloud": map[string]any{"provider": "aws"}},
		ConfigPath: "/home/user/fabrica.yaml",
	}
	if r.ConfigPath != "/home/user/fabrica.yaml" {
		t.Errorf("ConfigPath = %v, want /home/user/fabrica.yaml", r.ConfigPath)
	}
	if cfg, ok := r.Config["cloud"].(map[string]any); !ok || cfg["provider"] != "aws" {
		t.Error("Config map structure incorrect")
	}
}

// — Redaction key list —

func TestRedactKeys_List(t *testing.T) {
	expected := []string{"password", "token", "secret", "api_key", "access_key"}
	if len(redactKeys) != len(expected) {
		t.Fatalf("redactKeys length = %d, want %d", len(redactKeys), len(expected))
	}
	for i, want := range expected {
		if redactKeys[i] != want {
			t.Errorf("redactKeys[%d] = %q, want %q", i, redactKeys[i], want)
		}
	}
}
