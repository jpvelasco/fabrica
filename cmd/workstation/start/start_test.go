package start

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

func newTestCommand(out *bytes.Buffer, st *fabricastate.State, startErr error) command {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	c := command{
		runtime: globals.Runtime{Config: cfg, Provider: nil},
		out:     out,
		confirm: func(_ string) bool { return true },
	}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	c.writeState = func(_ *fabricastate.State) error { return nil }
	c.startInstance = func(_ context.Context, _ string) error { return startErr }
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

func TestStartNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, st, nil)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "not provisioned")
}

func TestStartAlreadyRunning(t *testing.T) {
	for _, status := range []string{"ready", "provisioning"} {
		t.Run(status, func(t *testing.T) {
			var out bytes.Buffer
			st := workstationState(status)
			startCalled := false
			c := newTestCommand(&out, st, nil)
			c.startInstance = func(_ context.Context, _ string) error {
				startCalled = true
				return nil
			}

			if err := c.run(context.Background()); err != nil {
				t.Fatalf("run: %v", err)
			}
			if startCalled {
				t.Error("start API should not be called when already running")
			}
			assertContains(t, out.String(), "already running")
		})
	}
}

func TestStartDryRunNoAPICall(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	startCalled := false
	c := newTestCommand(&out, st, nil)
	c.dryRun = true
	c.startInstance = func(_ context.Context, _ string) error {
		startCalled = true
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if startCalled {
		t.Error("dry-run must not call start API")
	}
	assertContains(t, out.String(), "dry run")
}

func TestStartDryRunOutputFields(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	c := newTestCommand(&out, st, nil)
	c.dryRun = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "i-ws123")
	assertContains(t, out.String(), "without --dry-run")
}

func TestStartHappyPathStateUpdated(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
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
		t.Fatal("workstation module should still exist after start")
	}
	if m.Status != "ready" {
		t.Errorf("status = %q, want ready", m.Status)
	}
}

func TestStartConfirmationRejected(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	startCalled := false
	c := newTestCommand(&out, st, nil)
	c.confirm = func(_ string) bool { return false }
	c.startInstance = func(_ context.Context, _ string) error {
		startCalled = true
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if startCalled {
		t.Error("start was called after confirmation rejected")
	}
	assertContains(t, out.String(), "Cancelled")
}

func TestStartNilProviderErrors(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.startInstance = nil

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error with nil startInstance")
	}
	assertContains(t, err.Error(), "no provider configured")
}

func TestStartAPIErrorPropagates(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	c := newTestCommand(&out, st, errors.New("permission denied"))
	c.assumeYes = true

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when start API fails")
	}
	assertContains(t, err.Error(), "starting instance")
}

func TestStartReadStateError(t *testing.T) {
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

func TestStartWriteStateErrorSurfacedAsWarning(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.writeState = func(_ *fabricastate.State) error {
		return errors.New("disk full")
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("writeState failure must not abort start: %v", err)
	}
	assertContains(t, out.String(), "Warning")
}

func TestStartJSONHappyPath(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.jsonOut = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var result StartOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if result.DryRun {
		t.Error("dryRun must be false")
	}
	if result.Status != "ready" {
		t.Errorf("status = %q, want ready", result.Status)
	}
	if result.InstanceID != "i-ws123" {
		t.Errorf("instanceId = %q, want i-ws123", result.InstanceID)
	}
}

func TestStartJSONDryRun(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	c := newTestCommand(&out, st, nil)
	c.dryRun = true
	c.jsonOut = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var result StartOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if !result.DryRun {
		t.Error("dryRun must be true")
	}
	if result.Status != "would_start" {
		t.Errorf("status = %q, want would_start", result.Status)
	}
}

func TestStartJSONAlreadyRunning(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	c := newTestCommand(&out, st, nil)
	c.jsonOut = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var result StartOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if result.Status != "already_running" {
		t.Errorf("status = %q, want already_running", result.Status)
	}
}

func TestStartAssumeYesSkipsPrompt(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
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
