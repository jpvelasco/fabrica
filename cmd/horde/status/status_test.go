package status

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/internal/config"
)

// These tests cover Horde-specific rendering (text + JSON) and port resolution.
// Shared orchestration is tested once in cmd/internal/modstatus; the command is
// exercised end-to-end in cobra_test.go.

func testRenderer() renderer { return renderer{port: defaultPort, grpcPort: defaultGRPCPort} }

func TestRenderText_RunningWithIP(t *testing.T) {
	var out bytes.Buffer
	testRenderer().printText(&out, modstatus.Info{
		ModuleStatus:   "provisioning",
		InstanceID:     "i-abc123",
		InstanceType:   "m7i.2xlarge",
		PrivateIP:      "10.0.1.42",
		InstanceState:  "running",
		ProbeAttempted: true,
		Reachable:      true,
	})
	got := out.String()
	for _, want := range []string{
		"Horde build coordinator", "(running)", "m7i.2xlarge", "10.0.1.42",
		"Horde HTTP:    http://10.0.1.42:5000", "Horde gRPC:    10.0.1.42:5002", "responding",
	} {
		assertContains(t, got, want)
	}
}

func TestRenderText_SettingUp(t *testing.T) {
	var out bytes.Buffer
	testRenderer().printText(&out, modstatus.Info{ModuleStatus: "provisioning", InstanceID: "i-1", SGID: "sg-1"})
	got := out.String()
	assertContains(t, got, "sg-1")
	assertContains(t, got, "setting up")
}

func TestRenderJSON_Fields(t *testing.T) {
	var out bytes.Buffer
	testRenderer().printJSON(&out, modstatus.Info{
		ModuleStatus:   "provisioning",
		InstanceID:     "i-abc123",
		SGID:           "sg-abc123",
		InstanceType:   "m7i.2xlarge",
		PrivateIP:      "10.0.1.42",
		ProbeAttempted: true,
		Reachable:      false,
	})
	var result StatusOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if !result.Provisioned {
		t.Error("expected provisioned=true")
	}
	if result.HordeURL != "http://10.0.1.42:5000" {
		t.Errorf("hordeUrl = %q", result.HordeURL)
	}
	if result.HordeGRPC != "10.0.1.42:5002" {
		t.Errorf("hordeGrpc = %q", result.HordeGRPC)
	}
	if result.HordeStatus != "unreachable" {
		t.Errorf("hordeStatus = %q, want unreachable", result.HordeStatus)
	}
	if result.SGID != "sg-abc123" {
		t.Errorf("sgId = %q", result.SGID)
	}
}

func TestRenderJSON_CustomPorts(t *testing.T) {
	var out bytes.Buffer
	renderer{port: 8080, grpcPort: 9090}.printJSON(&out, modstatus.Info{
		ModuleStatus: "ready",
		PrivateIP:    "10.0.0.5",
	})
	var result StatusOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result.HordeURL != "http://10.0.0.5:8080" {
		t.Errorf("hordeUrl = %q, want http://10.0.0.5:8080", result.HordeURL)
	}
	if result.HordeGRPC != "10.0.0.5:9090" {
		t.Errorf("hordeGrpc = %q, want 10.0.0.5:9090", result.HordeGRPC)
	}
}

func TestRenderNotProvisioned_JSON(t *testing.T) {
	var out bytes.Buffer
	renderer{}.NotProvisioned(&out, true)
	var result StatusOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result.Provisioned {
		t.Error("expected provisioned=false")
	}
	if result.Status != "not_provisioned" {
		t.Errorf("status = %q", result.Status)
	}
}

func TestResolvePorts(t *testing.T) {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg}
	if p, g := resolvePorts(rt); p != defaultPort || g != defaultGRPCPort {
		t.Errorf("defaults: got %d/%d, want %d/%d", p, g, defaultPort, defaultGRPCPort)
	}

	cfg.Horde.Port = 8080
	cfg.Horde.GRPCPort = 9090
	if p, g := resolvePorts(rt); p != 8080 || g != 9090 {
		t.Errorf("overrides: got %d/%d, want 8080/9090", p, g)
	}
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if len(substr) == 0 {
		return
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Fatalf("%q\ndoes not contain\n%q", s, substr)
}
