package stop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func newTestCommand(out *bytes.Buffer, st *fabricastate.State, stopErr error) command {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	c := command{
		runtime: globals.Runtime{Config: cfg, Provider: nil},
		out:     out,
		confirm: func(_ string) bool { return true },
	}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	c.writeState = func(_ *fabricastate.State) error { return nil }
	c.stopInstance = func(_ context.Context, _ string) error { return stopErr }
	return c
}

func workstationState(status string) *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("workstation", "ami-test", status, []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-ws123"},
		{TypeName: "AWS::EC2::Instance", Identifier: "i-ws123"},
	})
	return st
}

func TestStopNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, st, nil)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "not provisioned")
}

func TestStopAlreadyStopped(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	stopCalled := false
	c := newTestCommand(&out, st, nil)
	c.stopInstance = func(_ context.Context, _ string) error {
		stopCalled = true
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if stopCalled {
		t.Error("stop API should not be called when already stopped")
	}
	assertContains(t, out.String(), "already stopped")
}

func TestStopDryRunNoAPICall(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	stopCalled := false
	c := newTestCommand(&out, st, nil)
	c.dryRun = true
	c.stopInstance = func(_ context.Context, _ string) error {
		stopCalled = true
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if stopCalled {
		t.Error("dry-run must not call stop API")
	}
	assertContains(t, out.String(), "dry run")
}

func TestStopDryRunOutputFields(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	c := newTestCommand(&out, st, nil)
	c.dryRun = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "i-ws123")
	assertContains(t, out.String(), "without --dry-run")
}

func TestStopHappyPathStateUpdated(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	var lastState *fabricastate.State
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.writeState = func(s *fabricastate.State) error {
		cp := *s
		lastState = &cp
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if lastState == nil {
		t.Fatal("state was never written")
	}
	m := lastState.GetModule("workstation")
	if m == nil {
		t.Fatal("workstation module should still exist after stop")
	}
	if m.Status != "stopped" {
		t.Errorf("status = %q, want stopped", m.Status)
	}
}

func TestStopConfirmationRejected(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	stopCalled := false
	c := newTestCommand(&out, st, nil)
	c.confirm = func(_ string) bool { return false }
	c.stopInstance = func(_ context.Context, _ string) error {
		stopCalled = true
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if stopCalled {
		t.Error("stop was called after confirmation rejected")
	}
	assertContains(t, out.String(), "Cancelled")
}

func TestStopNilProviderErrors(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.stopInstance = nil

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error with nil stopInstance")
	}
	assertContains(t, err.Error(), "no provider configured")
}

func TestStopAPIErrorPropagates(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	c := newTestCommand(&out, st, errors.New("permission denied"))
	c.assumeYes = true

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when stop API fails")
	}
	assertContains(t, err.Error(), "stopping instance")
}

func TestStopReadStateError(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, nil, nil)
	c.readState = func() (*fabricastate.State, error) {
		return nil, errors.New("disk read failure")
	}

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when readState fails")
	}
	assertContains(t, err.Error(), "reading state")
}

func TestStopWriteStateErrorSurfacedAsWarning(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.writeState = func(_ *fabricastate.State) error {
		return errors.New("disk full")
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("writeState failure must not abort stop: %v", err)
	}
	assertContains(t, out.String(), "Warning")
}

func TestStopJSONHappyPath(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.jsonOut = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var result StopOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if result.DryRun {
		t.Error("dryRun must be false")
	}
	if result.Status != "stopped" {
		t.Errorf("status = %q, want stopped", result.Status)
	}
	if result.InstanceID != "i-ws123" {
		t.Errorf("instanceId = %q, want i-ws123", result.InstanceID)
	}
}

func TestStopJSONDryRun(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	c := newTestCommand(&out, st, nil)
	c.dryRun = true
	c.jsonOut = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var result StopOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if !result.DryRun {
		t.Error("dryRun must be true")
	}
	if result.Status != "would_stop" {
		t.Errorf("status = %q, want would_stop", result.Status)
	}
}

func TestStopJSONAlreadyStopped(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	c := newTestCommand(&out, st, nil)
	c.jsonOut = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var result StopOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if result.Status != "already_stopped" {
		t.Errorf("status = %q, want already_stopped", result.Status)
	}
}

func TestStopAssumeYesSkipsPrompt(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	confirmCalled := false
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.confirm = func(_ string) bool {
		confirmCalled = true
		return true
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if confirmCalled {
		t.Error("confirm must not be called when --yes is set")
	}
}

// ---- helpers ----

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Fatalf("%q does not contain %q", s, substr)
}
