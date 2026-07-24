package stop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/workstation/action"
	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestCommandStructure(t *testing.T) {
	cmd := New(
		func() (globals.Runtime, error) { return globals.Runtime{}, nil },
		func() globals.Options { return globals.Options{} },
		&bytes.Buffer{},
	)
	if cmd.Use != "stop" {
		t.Errorf("Use = %q, want stop", cmd.Use)
	}
	if cmd.Short != "Stop the cloud workstation EC2 instance" {
		t.Errorf("Short = %q", cmd.Short)
	}
}

func TestStopNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	ac := action.New(
		action.StopSpec,
		globals.Runtime{Config: config.Defaults()},
		false, false, false, &out,
		func(_, _ string) bool { return true },
		nil,
	)
	ac.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	ac.SetWriteState(func(*fabricastate.State) error { return nil })

	err := ac.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assert.Contains(t, out.String(), "not provisioned")
}

func TestStopAlreadyStopped(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	ac := action.New(
		action.StopSpec,
		globals.Runtime{Config: config.Defaults()},
		false, false, false, &out,
		func(_, _ string) bool { return true },
		nil,
	)
	ac.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	ac.SetWriteState(func(*fabricastate.State) error { return nil })

	err := ac.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assert.Contains(t, out.String(), "already stopped")
}

func TestStopDryRunNoAPICall(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	stopCalled := false
	ac := action.New(
		action.StopSpec,
		globals.Runtime{Config: config.Defaults()},
		true, false, false, &out,
		func(_, _ string) bool { return true },
		func(_ context.Context, _ string) error {
			stopCalled = true
			return nil
		},
	)
	ac.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	ac.SetWriteState(func(*fabricastate.State) error { return nil })

	err := ac.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if stopCalled {
		t.Error("dry-run must not call stop API")
	}
	assert.Contains(t, out.String(), "dry run")
}

func TestStopDryRunOutputFields(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	ac := action.New(
		action.StopSpec,
		globals.Runtime{Config: config.Defaults()},
		true, false, false, &out,
		func(_, _ string) bool { return true },
		nil,
	)
	ac.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	ac.SetWriteState(func(*fabricastate.State) error { return nil })

	err := ac.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assert.Contains(t, out.String(), "i-ws123")
	assert.Contains(t, out.String(), "without --dry-run")
}

func TestStopHappyPathStateUpdated(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	var lastState *fabricastate.State
	ac := action.New(
		action.StopSpec,
		globals.Runtime{Config: config.Defaults()},
		false, true, false, &out,
		func(_, _ string) bool { return true },
		func(_ context.Context, _ string) error { return nil },
	)
	ac.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	ac.SetWriteState(func(s *fabricastate.State) error {
		cp := *s
		lastState = &cp
		return nil
	})

	err := ac.Run(context.Background())
	if err != nil {
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
	ac := action.New(
		action.StopSpec,
		globals.Runtime{Config: config.Defaults()},
		false, false, false, &out,
		func(_, _ string) bool { return false },
		nil,
	)
	ac.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	ac.SetWriteState(func(*fabricastate.State) error { return nil })

	err := ac.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assert.Contains(t, out.String(), "Cancelled")
}

func TestStopNilProviderErrors(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	ac := action.New(
		action.StopSpec,
		globals.Runtime{Config: config.Defaults()},
		false, true, false, &out,
		func(_, _ string) bool { return true },
		nil,
	)
	ac.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	ac.SetWriteState(func(*fabricastate.State) error { return nil })

	err := ac.Run(context.Background())
	if err == nil {
		t.Fatal("expected error with nil executeAction")
	}
	assert.Contains(t, err.Error(), "no provider configured")
}

func TestStopAPIErrorPropagates(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	ac := action.New(
		action.StopSpec,
		globals.Runtime{Config: config.Defaults()},
		false, true, false, &out,
		func(_, _ string) bool { return true },
		func(_ context.Context, _ string) error { return errors.New("permission denied") },
	)
	ac.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	ac.SetWriteState(func(*fabricastate.State) error { return nil })

	err := ac.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when stop API fails")
	}
	assert.Contains(t, err.Error(), "stopping instance")
}

func TestStopReadStateError(t *testing.T) {
	var out bytes.Buffer
	ac := action.New(
		action.StopSpec,
		globals.Runtime{Config: config.Defaults()},
		false, false, false, &out,
		func(_, _ string) bool { return true },
		nil,
	)
	ac.SetReadState(func() (*fabricastate.State, error) {
		return nil, errors.New("disk read failure")
	})
	ac.SetWriteState(func(*fabricastate.State) error { return nil })

	err := ac.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when readState fails")
	}
	assert.Contains(t, err.Error(), "reading state")
}

func TestStopWriteStateErrorSurfacedAsWarning(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	ac := action.New(
		action.StopSpec,
		globals.Runtime{Config: config.Defaults()},
		false, true, false, &out,
		func(_, _ string) bool { return true },
		func(_ context.Context, _ string) error { return nil },
	)
	ac.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	ac.SetWriteState(func(*fabricastate.State) error {
		return errors.New("disk full")
	})

	err := ac.Run(context.Background())
	if err != nil {
		t.Fatalf("writeState failure must not abort stop: %v", err)
	}
	assert.Contains(t, out.String(), "Warning")
}

func TestStopJSONHappyPath(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	ac := action.New(
		action.StopSpec,
		globals.Runtime{Config: config.Defaults()},
		false, true, true, &out,
		func(_, _ string) bool { return true },
		func(_ context.Context, _ string) error { return nil },
	)
	ac.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	ac.SetWriteState(func(*fabricastate.State) error { return nil })

	err := ac.Run(context.Background())
	if err != nil {
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
}

func TestStopJSONDryRun(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	ac := action.New(
		action.StopSpec,
		globals.Runtime{Config: config.Defaults()},
		true, false, true, &out,
		func(_, _ string) bool { return true },
		nil,
	)
	ac.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	ac.SetWriteState(func(*fabricastate.State) error { return nil })

	err := ac.Run(context.Background())
	if err != nil {
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
	ac := action.New(
		action.StopSpec,
		globals.Runtime{Config: config.Defaults()},
		false, false, true, &out,
		func(_, _ string) bool { return true },
		nil,
	)
	ac.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	ac.SetWriteState(func(*fabricastate.State) error { return nil })

	err := ac.Run(context.Background())
	if err != nil {
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
	ac := action.New(
		action.StopSpec,
		globals.Runtime{Config: config.Defaults()},
		false, true, false, &out,
		func(_, _ string) bool {
			confirmCalled = true
			return true
		},
		func(_ context.Context, _ string) error { return nil },
	)
	ac.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	ac.SetWriteState(func(*fabricastate.State) error { return nil })

	err := ac.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if confirmCalled {
		t.Error("confirm must not be called when --yes is set")
	}
}

// ---- helpers ----

func workstationState(status string) *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("workstation", "ami-test", status, []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-ws123"},
		{TypeName: "AWS::EC2::Instance", Identifier: "i-ws123"},
	})
	return st
}
