package status

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/internal/assert"
)

// These tests cover Workstation-specific rendering (text + JSON). The shared
// orchestration (state read, probe, ready transition, wait) is tested once in
// cmd/internal/modstatus, and the command is exercised end-to-end in
// cobra_test.go.

func TestRenderText_Provisioning(t *testing.T) {
	var out bytes.Buffer
	printText(&out, modstatus.Info{
		ModuleStatus: "provisioning",
		Version:      "ami-0abc123",
		InstanceID:   "i-abc123",
		SGID:         "sg-abc123",
	})
	got := out.String()
	for _, want := range []string{"NICE DCV Workstation", "provisioning", "i-abc123", "sg-abc123", "setting up"} {
		assert.Contains(t, got, want)
	}
	// No private IP yet — the DCV connection line is not printed.
	assert.NoneOf(t, got, "https://")
}

func TestRenderText_RunningWithIP(t *testing.T) {
	var out bytes.Buffer
	printText(&out, modstatus.Info{
		ModuleStatus:   "provisioning",
		Version:        "ami-0abc123",
		InstanceID:     "i-abc123",
		InstanceType:   "g4dn.xlarge",
		PrivateIP:      "10.0.1.42",
		InstanceState:  "running",
		ProbeAttempted: true,
		Reachable:      true,
	})
	got := out.String()
	for _, want := range []string{"(running)", "g4dn.xlarge", "10.0.1.42", "https://10.0.1.42:8443", "responding"} {
		assert.Contains(t, got, want)
	}
}

func TestRenderText_Unreachable(t *testing.T) {
	var out bytes.Buffer
	printText(&out, modstatus.Info{
		ModuleStatus:   "provisioning",
		PrivateIP:      "10.0.1.42",
		ProbeAttempted: true,
		Reachable:      false,
	})
	assert.Contains(t, out.String(), "unreachable from this machine")
}

func TestRenderJSON_Fields(t *testing.T) {
	var out bytes.Buffer
	printJSON(&out, modstatus.Info{
		ModuleStatus:   "provisioning",
		Version:        "ami-0abc123",
		InstanceID:     "i-abc123",
		SGID:           "sg-abc123",
		InstanceType:   "g4dn.xlarge",
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
	if result.InstanceID != "i-abc123" {
		t.Errorf("instanceId = %q", result.InstanceID)
	}
	if result.PrivateIP != "10.0.1.42" {
		t.Errorf("privateIp = %q", result.PrivateIP)
	}
	if result.SGID != "sg-abc123" {
		t.Errorf("sgId = %q", result.SGID)
	}
	if result.InstanceType != "g4dn.xlarge" {
		t.Errorf("instanceType = %q", result.InstanceType)
	}
	if result.DCVURL != "https://10.0.1.42:8443" {
		t.Errorf("dcvUrl = %q, want https://10.0.1.42:8443", result.DCVURL)
	}
	if result.DCVStatus != "unreachable" {
		t.Errorf("dcvStatus = %q, want unreachable", result.DCVStatus)
	}
}

func TestRenderJSON_DCVStates(t *testing.T) {
	cases := []struct {
		name    string
		info    modstatus.Info
		wantDCV string
	}{
		{"responding", modstatus.Info{ModuleStatus: "ready", PrivateIP: "10.0.0.1", ProbeAttempted: true, Reachable: true}, "responding"},
		{"unreachable", modstatus.Info{ModuleStatus: "provisioning", PrivateIP: "10.0.0.1", ProbeAttempted: true, Reachable: false}, "unreachable"},
		{"setting_up", modstatus.Info{ModuleStatus: "provisioning"}, "setting up"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			printJSON(&out, tc.info)
			var result StatusOutput
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			if result.DCVStatus != tc.wantDCV {
				t.Errorf("dcvStatus = %q, want %q", result.DCVStatus, tc.wantDCV)
			}
		})
	}
}

func TestRenderJSON_NoIP(t *testing.T) {
	var out bytes.Buffer
	printJSON(&out, modstatus.Info{ModuleStatus: "provisioning", InstanceID: "i-abc123"})
	var result StatusOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if result.DCVURL != "" {
		t.Errorf("dcvUrl = %q, want empty without a private IP", result.DCVURL)
	}
	assert.NoneOf(t, out.String(), "8443")
}
