package status

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
		now:     time.Now,
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

// TestStatusReadStateError verifies error is surfaced immediately.
func TestStatusReadStateError(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, nil, nil, nil)
	c.readState = func() (*fabricastate.State, error) {
		return nil, errors.New("disk failure")
	}

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when readState fails")
	}
	assertContains(t, err.Error(), "reading state")
}

// TestStatusAlreadyReady verifies no state write when module is already ready.
func TestStatusAlreadyReady(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("ready", true)
	stateWritten := false
	getResource := func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"InstanceType":     "m5.xlarge",
			"PrivateIpAddress": "10.0.1.42",
			"State":            map[string]any{"Name": "running"},
		})
		return nil
	}
	c := newTestCommand(&out, st, getResource, func(string) bool { return true })
	c.writeState = func(_ *fabricastate.State) error {
		stateWritten = true
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if stateWritten {
		t.Error("state must not be rewritten when module is already ready")
	}
	assertContains(t, out.String(), "ready")
}

// TestStatusWriteStateErrorSurfacedAsWarning verifies write errors show a warning but don't fail.
func TestStatusWriteStateErrorSurfacedAsWarning(t *testing.T) {
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
	c := newTestCommand(&out, st, getResource, func(string) bool { return true })
	c.writeState = func(_ *fabricastate.State) error {
		return errors.New("disk full")
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("status must not return error on writeState failure: %v", err)
	}
	assertContains(t, out.String(), "Warning")
	assertContains(t, out.String(), "disk full")
}

// TestStatusWaitBecomesReady verifies --wait exits on first successful probe.
func TestStatusWaitBecomesReady(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	probeCall := 0
	getResource := func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"InstanceType":     "m5.xlarge",
			"PrivateIpAddress": "10.0.1.42",
			"State":            map[string]any{"Name": "running"},
		})
		return nil
	}
	c := newTestCommand(&out, st, getResource, func(string) bool {
		probeCall++
		return probeCall >= 2 // fail once, succeed on second call
	})
	c.wait = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "ready")
	assertContains(t, out.String(), "responding")
	if probeCall < 2 {
		t.Errorf("expected at least 2 probe calls, got %d", probeCall)
	}
}

// TestStatusWaitTimeout verifies --wait surfaces timeout message after deadline.
func TestStatusWaitTimeout(t *testing.T) {
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
	// Time starts at deadline - 1s so the very first iteration exceeds the deadline.
	startTime := time.Now()
	callCount := 0
	c := newTestCommand(&out, st, getResource, func(string) bool { return false })
	c.wait = true
	c.now = func() time.Time {
		callCount++
		if callCount <= 1 {
			return startTime // first call: compute deadline
		}
		return startTime.Add(waitDeadline + time.Second) // subsequent: past deadline
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "Timed out")
}

// TestStatusWaitGetResourceError surfaces error during poll loop.
func TestStatusWaitGetResourceError(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	getResource := func(_ context.Context, r *cloud.Resource) error {
		return &fakeProviderError{"network timeout"}
	}
	c := newTestCommand(&out, st, getResource, nil)
	c.wait = true

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when getResource fails during wait")
	}
	assertContains(t, err.Error(), "querying instance")
}

// TestStatusProbeAddressFormat verifies probe is called with "ip:1666".
func TestStatusProbeAddressFormat(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	var probeAddr string
	getResource := func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"InstanceType":     "m5.xlarge",
			"PrivateIpAddress": "192.168.1.10",
			"State":            map[string]any{"Name": "running"},
		})
		return nil
	}
	c := newTestCommand(&out, st, getResource, func(addr string) bool {
		probeAddr = addr
		return false
	})

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if probeAddr != "192.168.1.10:1666" {
		t.Errorf("probe address = %q, want 192.168.1.10:1666", probeAddr)
	}
}

// TestStatusNoProbeWhenNoPrivateIP verifies probe is not attempted without an IP.
func TestStatusNoProbeWhenNoPrivateIP(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	probeCalled := false
	getResource := func(_ context.Context, r *cloud.Resource) error {
		// Return non-empty state but no PrivateIpAddress.
		r.ActualState = mustMarshal(map[string]any{
			"InstanceType": "m5.xlarge",
			"State":        map[string]any{"Name": "pending"},
		})
		return nil
	}
	c := newTestCommand(&out, st, getResource, func(string) bool {
		probeCalled = true
		return true
	})

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if probeCalled {
		t.Error("TCP probe must not be attempted when instance has no private IP")
	}
}

// TestStatusJSONHelixCoreField verifies helixCore values in JSON output.
func TestStatusJSONHelixCoreField(t *testing.T) {
	cases := []struct {
		name          string
		probeResult   bool
		moduleStatus  string
		wantHelixCore string
	}{
		{"responding", true, "provisioning", "responding"},
		{"unreachable", false, "provisioning", "unreachable"},
		{"setting_up_no_ip", false, "provisioning", "setting up"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			st := perforceState(tc.moduleStatus, true)

			var getResource func(context.Context, *cloud.Resource) error
			if tc.name == "setting_up_no_ip" {
				getResource = func(_ context.Context, r *cloud.Resource) error { return nil }
			} else {
				getResource = func(_ context.Context, r *cloud.Resource) error {
					r.ActualState = mustMarshal(map[string]any{
						"InstanceType":     "m5.xlarge",
						"PrivateIpAddress": "10.0.0.1",
						"State":            map[string]any{"Name": "running"},
					})
					return nil
				}
			}

			c := newTestCommand(&out, st, getResource, func(string) bool { return tc.probeResult })
			c.jsonOut = true

			if err := c.run(context.Background()); err != nil {
				t.Fatalf("run: %v", err)
			}
			var result StatusOutput
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
			}
			if result.HelixCore != tc.wantHelixCore {
				t.Errorf("helixCore = %q, want %q", result.HelixCore, tc.wantHelixCore)
			}
		})
	}
}

// TestStatusJSONSGID verifies sgId appears in JSON output.
func TestStatusJSONSGID(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true) // includes sg-abc123
	c := newTestCommand(&out, st, nil, nil)
	c.jsonOut = true
	// No getResource — no instance IP, so probe won't fire.

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var result StatusOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if result.SGID != "sg-abc123" {
		t.Errorf("sgId = %q, want sg-abc123", result.SGID)
	}
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
