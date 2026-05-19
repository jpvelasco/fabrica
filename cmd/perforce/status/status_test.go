package status

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// newTestCommand builds a status command with injected state and no-op provider.
func newTestCommand(out *bytes.Buffer, st *fabricastate.State, getResource func(context.Context, *cloud.Resource) error, probe func(string) bool) command {
	cfg := config.Defaults()
	c := command{
		runtime: globals.Runtime{Config: cfg, Provider: nil},
		out:     out,
		sleep:   func(time.Duration) {},
	}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	c.writeState = func(_ *fabricastate.State) error { return nil }
	c.getResource = getResource
	if probe != nil {
		c.probeTCP = probe
	} else {
		c.probeTCP = func(string) bool { return false }
	}
	return c
}

func perforceState(status string, withInstance bool) *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	resources := []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-abc123"},
	}
	if withInstance {
		resources = append(resources, fabricastate.ModuleResource{
			TypeName:   "AWS::EC2::Instance",
			Identifier: "i-abc123",
		})
	}
	st.UpsertModule("perforce", "2024.2", status, resources)
	return st
}

// TestStatusNotProvisioned verifies clean output when no module is in state.
func TestStatusNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, st, nil, nil)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "not provisioned")
}

// TestStatusProvisioningNoPrivateIP verifies output when instance has no IP yet.
func TestStatusProvisioningNoPrivateIP(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	getResource := func(_ context.Context, r *cloud.Resource) error {
		// Return empty ActualState — Cloud Control still processing.
		return nil
	}
	c := newTestCommand(&out, st, getResource, nil)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	assertContains(t, got, "provisioning")
	assertContains(t, got, "i-abc123")
	assertContains(t, got, "setting up")
}

// TestStatusRunningInstanceStateShown verifies instance state label appears.
func TestStatusRunningInstanceStateShown(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	getResource := func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"InstanceType":     "m5.xlarge",
			"PrivateIpAddress": "10.0.1.42",
			"State":            map[string]any{"Name": "running"},
		})
		return nil
	}
	c := newTestCommand(&out, st, getResource, func(string) bool { return false })

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	assertContains(t, got, "i-abc123")
	assertContains(t, got, "(running)")
	assertContains(t, got, "10.0.1.42")
	assertContains(t, got, "m5.xlarge")
}

// TestStatusTCPProbeSuccessTransitionsToReady verifies state is updated and output shows ready.
func TestStatusTCPProbeSuccessTransitionsToReady(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	var writtenStatus string
	getResource := func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"InstanceType":     "m5.xlarge",
			"PrivateIpAddress": "10.0.1.42",
			"State":            map[string]any{"Name": "running"},
		})
		return nil
	}
	c := newTestCommand(&out, st, getResource, func(string) bool { return true })
	c.writeState = func(s *fabricastate.State) error {
		if m := s.GetModule("perforce"); m != nil {
			writtenStatus = m.Status
		}
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	assertContains(t, got, "ready")
	assertContains(t, got, "responding")
	if writtenStatus != "ready" {
		t.Errorf("state written with status %q, want %q", writtenStatus, "ready")
	}
}

// TestStatusGetResourceError verifies error is surfaced when provider.Get fails.
func TestStatusGetResourceError(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	getResource := func(_ context.Context, r *cloud.Resource) error {
		return &fakeProviderError{"describe failed"}
	}
	c := newTestCommand(&out, st, getResource, nil)

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when getResource fails")
	}
	assertContains(t, err.Error(), "querying instance")
}

// TestStatusOnlySGNoInstance verifies graceful output with partial state (SG only).
func TestStatusOnlySGNoInstance(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", false) // no instance resource
	c := newTestCommand(&out, st, nil, nil)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	assertContains(t, got, "provisioning")
	assertContains(t, got, "sg-abc123")
}

// TestStatusJSONNotProvisioned verifies JSON output when not provisioned.
func TestStatusJSONNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, st, nil, nil)
	c.jsonOut = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var result StatusOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if result.Provisioned {
		t.Error("expected provisioned=false")
	}
	if result.Status != "not_provisioned" {
		t.Errorf("status = %q, want %q", result.Status, "not_provisioned")
	}
}

// TestStatusJSONProvisioned verifies JSON output contains expected fields.
func TestStatusJSONProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	getResource := func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"InstanceType":     "m5.xlarge",
			"PrivateIpAddress": "10.0.1.42",
			"State":            map[string]any{"Name": "running"},
		})
		return nil
	}
	c := newTestCommand(&out, st, getResource, func(string) bool { return false })
	c.jsonOut = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var result StatusOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if !result.Provisioned {
		t.Error("expected provisioned=true")
	}
	if result.InstanceID != "i-abc123" {
		t.Errorf("instanceId = %q, want %q", result.InstanceID, "i-abc123")
	}
	if result.PrivateIP != "10.0.1.42" {
		t.Errorf("privateIp = %q, want %q", result.PrivateIP, "10.0.1.42")
	}
	if result.P4PORT != "tcp:10.0.1.42:1666" {
		t.Errorf("p4port = %q, want %q", result.P4PORT, "tcp:10.0.1.42:1666")
	}
}

// TestStatusUnreachableFromMachine verifies the "unreachable" message when probe fails.
func TestStatusUnreachableFromMachine(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	getResource := func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"InstanceType":     "m5.xlarge",
			"PrivateIpAddress": "10.0.1.42",
			"State":            map[string]any{"Name": "running"},
		})
		return nil
	}
	c := newTestCommand(&out, st, getResource, func(string) bool { return false })

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "unreachable from this machine")
}

// ---- helpers ----

func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
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

type fakeProviderError struct{ msg string }

func (e *fakeProviderError) Error() string { return e.msg }
