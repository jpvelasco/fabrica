package start

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
	if cmd.Use != "start" {
		t.Errorf("Use = %q, want start", cmd.Use)
	}
	if cmd.Short != "Start a stopped cloud workstation EC2 instance" {
		t.Errorf("Short = %q", cmd.Short)
	}
}

func TestStartNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	ac := action.New(
		action.StartSpec,
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

func TestStartAlreadyRunning(t *testing.T) {
	for _, status := range []string{"ready", "provisioning"} {
		t.Run(status, func(t *testing.T) {
			var out bytes.Buffer
			st := workstationState(status)
			ac := action.New(
				action.StartSpec,
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
			assert.Contains(t, out.String(), "already running")
		})
	}
}

func TestStartDryRunNoAPICall(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	startCalled := false
	ac := action.New(
		action.StartSpec,
		globals.Runtime{Config: config.Defaults()},
		true, false, false, &out,
		func(_, _ string) bool { return true },
		func(_ context.Context, _ string) error {
			startCalled = true
			return nil
		},
	)
	ac.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	ac.SetWriteState(func(*fabricastate.State) error { return nil })

	err := ac.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if startCalled {
		t.Error("dry-run must not call start API")
	}
	assert.Contains(t, out.String(), "dry run")
}

func TestStartDryRunOutputFields(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	ac := action.New(
		action.StartSpec,
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

func TestStartHappyPathStateUpdated(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	var lastState *fabricastate.State
	ac := action.New(
		action.StartSpec,
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
		t.Fatal("workstation module should still exist after start")
	}
	if m.Status != "ready" {
		t.Errorf("status = %q, want ready", m.Status)
	}
}

func TestStartConfirmationRejected(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	ac := action.New(
		action.StartSpec,
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

func TestStartNilProviderErrors(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	ac := action.New(
		action.StartSpec,
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

func TestStartAPIErrorPropagates(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	ac := action.New(
		action.StartSpec,
		globals.Runtime{Config: config.Defaults()},
		false, true, false, &out,
		func(_, _ string) bool { return true },
		func(_ context.Context, _ string) error { return errors.New("permission denied") },
	)
	ac.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	ac.SetWriteState(func(*fabricastate.State) error { return nil })

	err := ac.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when start API fails")
	}
	assert.Contains(t, err.Error(), "starting instance")
}

func TestStartReadStateError(t *testing.T) {
	var out bytes.Buffer
	ac := action.New(
		action.StartSpec,
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

func TestStartWriteStateErrorSurfacedAsWarning(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	ac := action.New(
		action.StartSpec,
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
		t.Fatalf("writeState failure must not abort start: %v", err)
	}
	assert.Contains(t, out.String(), "Warning")
}

func TestStartJSONHappyPath(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	ac := action.New(
		action.StartSpec,
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
}

func TestStartJSONDryRun(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	ac := action.New(
		action.StartSpec,
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
	ac := action.New(
		action.StartSpec,
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
	ac := action.New(
		action.StartSpec,
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
