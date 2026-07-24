package status

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/internal/assert"
)

// These tests cover Perforce-specific rendering (text + JSON). The shared
// orchestration (state read, probe, ready transition, wait) is tested once in
// cmd/internal/modstatus, and the command is exercised end-to-end in cobra_test.go.

func TestRenderText_Provisioning(t *testing.T) {
	var out bytes.Buffer
	printText(&out, modstatus.Info{
		ModuleStatus: "provisioning",
		Version:      "2024.2",
		InstanceID:   "i-abc123",
		SGID:         "sg-abc123",
	})
	got := out.String()
	for _, want := range []string{"Perforce Helix Core", "provisioning", "i-abc123", "sg-abc123", "setting up"} {
		assert.Contains(t, got, want)
	}
}

func TestRenderText_RunningWithIP(t *testing.T) {
	var out bytes.Buffer
	printText(&out, modstatus.Info{
		ModuleStatus:   "provisioning",
		Version:        "2024.2",
		InstanceID:     "i-abc123",
		InstanceType:   "m5.xlarge",
		PrivateIP:      "10.0.1.42",
		InstanceState:  "running",
		ProbeAttempted: true,
		Reachable:      true,
	})
	got := out.String()
	for _, want := range []string{"(running)", "m5.xlarge", "10.0.1.42", "P4PORT:        tcp:10.0.1.42:1666", "responding"} {
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

func TestRenderText_LastBackup(t *testing.T) {
	var out bytes.Buffer
	printText(&out, modstatus.Info{
		ModuleStatus: "ready",
		LastBackupId: "20260715-120000",
		LastBackupAt: "2026-07-15T12:00:00Z",
	})
	got := out.String()
	assert.Contains(t, got, "Last backup")
	assert.Contains(t, got, "20260715-120000")
	assert.Contains(t, got, "2026-07-15T12:00:00Z")
}

func TestRenderText_LastBackupNever(t *testing.T) {
	var out bytes.Buffer
	printText(&out, modstatus.Info{ModuleStatus: "ready"})
	assert.Contains(t, out.String(), "Last backup:   never")
}

func TestRenderJSON_LastBackup(t *testing.T) {
	var out bytes.Buffer
	printJSON(&out, modstatus.Info{
		ModuleStatus: "ready",
		LastBackupId: "id1",
		LastBackupAt: "t1",
	})
	var result StatusOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.LastBackupId != "id1" || result.LastBackupAt != "t1" {
		t.Fatalf("%+v", result)
	}
}

func TestRenderJSON_Fields(t *testing.T) {
	var out bytes.Buffer
	printJSON(&out, modstatus.Info{
		ModuleStatus:   "provisioning",
		Version:        "2024.2",
		InstanceID:     "i-abc123",
		SGID:           "sg-abc123",
		InstanceType:   "m5.xlarge",
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
	if result.P4PORT != "tcp:10.0.1.42:1666" {
		t.Errorf("p4port = %q, want tcp:10.0.1.42:1666", result.P4PORT)
	}
	if result.SGID != "sg-abc123" {
		t.Errorf("sgId = %q", result.SGID)
	}
	if result.HelixCore != "unreachable" {
		t.Errorf("helixCore = %q, want unreachable", result.HelixCore)
	}
}

func TestRenderJSON_HelixCoreStates(t *testing.T) {
	cases := []struct {
		name      string
		info      modstatus.Info
		wantHelix string
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
			if result.HelixCore != tc.wantHelix {
				t.Errorf("helixCore = %q, want %q", result.HelixCore, tc.wantHelix)
			}
		})
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
