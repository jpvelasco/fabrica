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

func hordeState(status string, withInstance bool) *fabricastate.State {
	return hordeStateWithVersion(status, withInstance, "ami-test123")
}

func hordeStateWithVersion(status string, withInstance bool, amiID string) *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	resources := []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-horde123"},
	}
	if withInstance {
		resources = append(resources, fabricastate.ModuleResource{
			TypeName:   "AWS::EC2::Instance",
			Identifier: "i-horde123",
		})
	}
	st.UpsertModule("horde", amiID, status, resources)
	return st
}

func mustMarshal(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
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

// TestStatusProvisioningNoIP verifies output when instance has no IP yet.
func TestStatusProvisioningNoIP(t *testing.T) {
	var out bytes.Buffer
	st := hordeState("provisioning", true)
	getResource := func(_ context.Context, r *cloud.Resource) error { return nil }
	c := newTestCommand(&out, st, getResource, nil)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	assertContains(t, got, "provisioning")
	assertContains(t, got, "i-horde123")
	assertContains(t, got, "setting up")
}

// TestStatusRunningInstanceStateShown verifies instance state label appears.
func TestStatusRunningInstanceStateShown(t *testing.T) {
	var out bytes.Buffer
	st := hordeState("provisioning", true)
	getResource := func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"InstanceType":     "m7i.xlarge",
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
	assertContains(t, got, "i-horde123")
	assertContains(t, got, "(running)")
	assertContains(t, got, "10.0.1.42")
	assertContains(t, got, "m7i.xlarge")
}

// TestStatusTCPProbeSuccessTransitionsToReady verifies state is updated and output shows ready.
func TestStatusTCPProbeSuccessTransitionsToReady(t *testing.T) {
	var out bytes.Buffer
	st := hordeState("provisioning", true)
	var writtenStatus string
	getResource := func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"InstanceType":     "m7i.xlarge",
			"PrivateIpAddress": "10.0.1.42",
			"State":            map[string]any{"Name": "running"},
		})
		return nil
	}
	c := newTestCommand(&out, st, getResource, func(string) bool { return true })
	c.writeState = func(s *fabricastate.State) error {
		if m := s.GetModule("horde"); m != nil {
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
		t.Errorf("state written with status %q, want ready", writtenStatus)
	}
}

// TestStatusProbeAddressFormat verifies probe is called with "ip:5000".
func TestStatusProbeAddressFormat(t *testing.T) {
	var out bytes.Buffer
	st := hordeState("provisioning", true)
	var probeAddr string
	getResource := func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
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
	if probeAddr != "192.168.1.10:5000" {
		t.Errorf("probe address = %q, want 192.168.1.10:5000", probeAddr)
	}
}

// TestStatusProbeUsesConfigPort verifies probe uses port from config when set.
func TestStatusProbeUsesConfigPort(t *testing.T) {
	var out bytes.Buffer
	st := hordeState("provisioning", true)
	var probeAddr string
	getResource := func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"PrivateIpAddress": "10.0.0.1",
			"State":            map[string]any{"Name": "running"},
		})
		return nil
	}
	c := newTestCommand(&out, st, getResource, func(addr string) bool {
		probeAddr = addr
		return false
	})
	c.runtime.Config.Horde.Port = 8080

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if probeAddr != "10.0.0.1:8080" {
		t.Errorf("probe address = %q, want 10.0.0.1:8080", probeAddr)
	}
}

// TestStatusGetResourceError verifies error is surfaced when provider.Get fails.
func TestStatusGetResourceError(t *testing.T) {
	var out bytes.Buffer
	st := hordeState("provisioning", true)
	getResource := func(_ context.Context, r *cloud.Resource) error {
		return errors.New("describe failed")
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
	st := hordeState("provisioning", false)
	c := newTestCommand(&out, st, nil, nil)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	assertContains(t, got, "provisioning")
	assertContains(t, got, "sg-horde123")
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
		t.Errorf("status = %q, want not_provisioned", result.Status)
	}
}

// TestStatusJSONHordeURLField verifies hordeUrl and hordeGrpc fields.
func TestStatusJSONHordeURLField(t *testing.T) {
	var out bytes.Buffer
	st := hordeState("ready", true)
	getResource := func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"InstanceType":     "m7i.xlarge",
			"PrivateIpAddress": "10.0.1.42",
			"State":            map[string]any{"Name": "running"},
		})
		return nil
	}
	c := newTestCommand(&out, st, getResource, func(string) bool { return true })
	c.jsonOut = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var result StatusOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if result.HordeURL != "http://10.0.1.42:5000" {
		t.Errorf("hordeUrl = %q, want http://10.0.1.42:5000", result.HordeURL)
	}
	if result.HordeGRPC != "10.0.1.42:5002" {
		t.Errorf("hordeGrpc = %q, want 10.0.1.42:5002", result.HordeGRPC)
	}
}

// TestStatusAlreadyReady verifies no state write when module is already ready.
func TestStatusAlreadyReady(t *testing.T) {
	var out bytes.Buffer
	st := hordeState("ready", true)
	stateWritten := false
	getResource := func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"InstanceType":     "m7i.xlarge",
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

// TestStatusVersionPreservedOnTransitionToReady verifies AMI version is not lost when state transitions to ready.
func TestStatusVersionPreservedOnTransitionToReady(t *testing.T) {
	var out bytes.Buffer
	st := hordeStateWithVersion("provisioning", true, "ami-preserve99")
	var writtenVersion string
	getResource := func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"PrivateIpAddress": "10.0.1.42",
			"State":            map[string]any{"Name": "running"},
		})
		return nil
	}
	c := newTestCommand(&out, st, getResource, func(string) bool { return true })
	c.writeState = func(s *fabricastate.State) error {
		if m := s.GetModule("horde"); m != nil {
			writtenVersion = m.Version
		}
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if writtenVersion != "ami-preserve99" {
		t.Errorf("version written = %q, want ami-preserve99", writtenVersion)
	}
}

// TestStatusWriteStateErrorSurfacedAsWarning verifies write errors show a warning but don't fail.
func TestStatusWriteStateErrorSurfacedAsWarning(t *testing.T) {
	var out bytes.Buffer
	st := hordeState("provisioning", true)
	getResource := func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"InstanceType":     "m7i.xlarge",
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

// TestStatusNoProbeWhenNoPrivateIP verifies probe is not attempted without an IP.
func TestStatusNoProbeWhenNoPrivateIP(t *testing.T) {
	var out bytes.Buffer
	st := hordeState("provisioning", true)
	probeCalled := false
	getResource := func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"InstanceType": "m7i.xlarge",
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

// TestStatusWaitBecomesReady verifies --wait exits on successful probe.
func TestStatusWaitBecomesReady(t *testing.T) {
	var out bytes.Buffer
	st := hordeState("provisioning", true)
	probeCall := 0
	getResource := func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"PrivateIpAddress": "10.0.1.42",
			"State":            map[string]any{"Name": "running"},
		})
		return nil
	}
	c := newTestCommand(&out, st, getResource, func(string) bool {
		probeCall++
		return probeCall >= 2
	})
	c.wait = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "ready")
	assertContains(t, out.String(), "responding")
}

// TestStatusWaitTimeout verifies --wait surfaces timeout message.
func TestStatusWaitTimeout(t *testing.T) {
	var out bytes.Buffer
	st := hordeState("provisioning", true)
	getResource := func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = mustMarshal(map[string]any{
			"PrivateIpAddress": "10.0.1.42",
			"State":            map[string]any{"Name": "running"},
		})
		return nil
	}
	startTime := time.Now()
	callCount := 0
	c := newTestCommand(&out, st, getResource, func(string) bool { return false })
	c.wait = true
	c.now = func() time.Time {
		callCount++
		if callCount <= 1 {
			return startTime
		}
		return startTime.Add(waitDeadline + time.Second)
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "Timed out")
}

// TestStatusWaitGetResourceError surfaces error during poll loop.
func TestStatusWaitGetResourceError(t *testing.T) {
	var out bytes.Buffer
	st := hordeState("provisioning", true)
	getResource := func(_ context.Context, r *cloud.Resource) error {
		return errors.New("network timeout")
	}
	c := newTestCommand(&out, st, getResource, nil)
	c.wait = true

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when getResource fails during wait")
	}
	assertContains(t, err.Error(), "querying instance")
}

// TestStatusJSONHordeStatusField verifies hordeStatus values in JSON output.
func TestStatusJSONHordeStatusField(t *testing.T) {
	cases := []struct {
		name            string
		probeResult     bool
		withIP          bool
		wantHordeStatus string
	}{
		{"responding", true, true, "responding"},
		{"unreachable", false, true, "unreachable"},
		{"setting_up_no_ip", false, false, "setting up"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			st := hordeState("provisioning", true)
			getResource := func(_ context.Context, r *cloud.Resource) error {
				if tc.withIP {
					r.ActualState = mustMarshal(map[string]any{
						"PrivateIpAddress": "10.0.0.1",
						"State":            map[string]any{"Name": "running"},
					})
				}
				return nil
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
			if result.HordeStatus != tc.wantHordeStatus {
				t.Errorf("hordeStatus = %q, want %q", result.HordeStatus, tc.wantHordeStatus)
			}
		})
	}
}

// TestStatusJSONSGID verifies sgId appears in JSON output.
func TestStatusJSONSGID(t *testing.T) {
	var out bytes.Buffer
	st := hordeState("provisioning", true)
	c := newTestCommand(&out, st, nil, nil)
	c.jsonOut = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var result StatusOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if result.SGID != "sg-horde123" {
		t.Errorf("sgId = %q, want sg-horde123", result.SGID)
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
