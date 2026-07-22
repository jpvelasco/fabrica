package action

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func workstationState(status string) *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("workstation", "ami-123", status, []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-abc"},
	})
	return st
}

func TestRunNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := New(
		StartSpec,
		globals.Runtime{Config: config.Defaults()},
		false, false, false, &out,
		func(_, _ string) bool { return false },
		nil,
	)
	c.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	c.SetWriteState(func(*fabricastate.State) error { return nil })

	err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "not provisioned") {
		t.Errorf("out: %s", out.String())
	}
}

func TestRunAlreadyRunning(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	c := New(
		StartSpec,
		globals.Runtime{Config: config.Defaults()},
		false, false, false, &out,
		func(_, _ string) bool { return false },
		nil,
	)
	c.SetReadState(func() (*fabricastate.State, error) { return st, nil })

	err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already running") {
		t.Errorf("out: %s", out.String())
	}
}

func TestRunAlreadyStopped(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	c := New(
		StopSpec,
		globals.Runtime{Config: config.Defaults()},
		false, false, false, &out,
		func(_, _ string) bool { return false },
		nil,
	)
	c.SetReadState(func() (*fabricastate.State, error) { return st, nil })

	err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "already stopped") {
		t.Errorf("out: %s", out.String())
	}
}

func TestRunDryRunStart(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	c := New(
		StartSpec,
		globals.Runtime{Config: config.Defaults()},
		true, false, false, &out,
		func(_, _ string) bool { return false },
		nil,
	)
	c.SetReadState(func() (*fabricastate.State, error) { return st, nil })

	err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "dry run") {
		t.Errorf("out: %s", out.String())
	}
}

func TestRunDryRunStop(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready")
	c := New(
		StopSpec,
		globals.Runtime{Config: config.Defaults()},
		true, false, false, &out,
		func(_, _ string) bool { return false },
		nil,
	)
	c.SetReadState(func() (*fabricastate.State, error) { return st, nil })

	err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "dry run") {
		t.Errorf("out: %s", out.String())
	}
}

func TestRunConfirmReject(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	c := New(
		StartSpec,
		globals.Runtime{Config: config.Defaults()},
		false, false, false, &out,
		func(_, _ string) bool { return false },
		nil,
	)
	c.SetReadState(func() (*fabricastate.State, error) { return st, nil })

	err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Cancelled") {
		t.Errorf("out: %s", out.String())
	}
}

func TestRunSuccess(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	called := false
	c := New(
		StartSpec,
		globals.Runtime{Config: config.Defaults()},
		false, true, false, &out,
		func(_, _ string) bool { return true },
		func(_ context.Context, id string) error {
			called = true
			return nil
		},
	)
	c.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	c.SetWriteState(func(s *fabricastate.State) error {
		if s.Modules[0].Status != "ready" {
			t.Errorf("status = %s, want ready", s.Modules[0].Status)
		}
		return nil
	})

	err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Error("executeAction should have been called")
	}
	if !strings.Contains(out.String(), "started") {
		t.Errorf("out: %s", out.String())
	}
}

func TestRunExecuteError(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	c := New(
		StartSpec,
		globals.Runtime{Config: config.Defaults()},
		false, true, false, &out,
		func(_, _ string) bool { return true },
		func(_ context.Context, _ string) error {
			return fmt.Errorf("stop failed")
		},
	)
	c.SetReadState(func() (*fabricastate.State, error) { return st, nil })

	err := c.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("err: %v", err)
	}
}

func TestRunNoExecuteAction(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	c := New(
		StartSpec,
		globals.Runtime{Config: config.Defaults()},
		false, true, false, &out,
		func(_, _ string) bool { return true },
		nil,
	)
	c.SetReadState(func() (*fabricastate.State, error) { return st, nil })

	err := c.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no provider") {
		t.Errorf("err: %v", err)
	}
}

func TestRunJSONNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := New(
		StartSpec,
		globals.Runtime{Config: config.Defaults()},
		false, false, true, &out,
		func(_, _ string) bool { return false },
		nil,
	)
	c.SetReadState(func() (*fabricastate.State, error) { return st, nil })

	err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "not_provisioned") {
		t.Errorf("out: %s", out.String())
	}
}

func TestRunJSONSuccess(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	c := New(
		StartSpec,
		globals.Runtime{Config: config.Defaults()},
		false, true, true, &out,
		func(_, _ string) bool { return true },
		func(_ context.Context, _ string) error { return nil },
	)
	c.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	c.SetWriteState(func(*fabricastate.State) error { return nil })

	err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ready") {
		t.Errorf("out: %s", out.String())
	}
}

func TestReadStateError(t *testing.T) {
	c := New(
		StartSpec,
		globals.Runtime{Config: config.Defaults()},
		false, false, false, &bytes.Buffer{},
		func(_, _ string) bool { return false },
		nil,
	)
	c.SetReadState(func() (*fabricastate.State, error) {
		return nil, fmt.Errorf("no state")
	})

	err := c.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "reading state") {
		t.Errorf("err: %v", err)
	}
}

func TestNoInstanceInState(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("workstation", "ami-123", "stopped", []fabricastate.ModuleResource{})
	c := New(
		StartSpec,
		globals.Runtime{Config: config.Defaults()},
		false, false, false, &out,
		func(_, _ string) bool { return false },
		nil,
	)
	c.SetReadState(func() (*fabricastate.State, error) { return st, nil })

	err := c.Run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestWriteStateWarning(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("stopped")
	c := New(
		StartSpec,
		globals.Runtime{Config: config.Defaults()},
		false, true, false, &out,
		func(_, _ string) bool { return true },
		func(_ context.Context, _ string) error { return nil },
	)
	c.SetReadState(func() (*fabricastate.State, error) { return st, nil })
	c.SetWriteState(func(*fabricastate.State) error {
		return fmt.Errorf("write failed")
	})

	err := c.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Warning") {
		t.Errorf("expected warning in output: %s", out.String())
	}
}

func TestStopSpecIsAlreadyActive(t *testing.T) {
	if !StopSpec.IsAlreadyActive("stopped") {
		t.Error("stopped should be already active for stop")
	}
	if StopSpec.IsAlreadyActive("ready") {
		t.Error("ready should not be already active for stop")
	}
}

func TestStartSpecIsAlreadyActive(t *testing.T) {
	if !StartSpec.IsAlreadyActive("ready") {
		t.Error("ready should be already active for start")
	}
	if !StartSpec.IsAlreadyActive("provisioning") {
		t.Error("provisioning should be already active for start")
	}
	if StartSpec.IsAlreadyActive("stopped") {
		t.Error("stopped should not be already active for start")
	}
}

func TestDefaultReadStateForRuntime(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	rt := globals.Runtime{Config: config.Defaults()}
	st, err := DefaultReadStateForRuntime(rt)
	if err != nil {
		t.Fatalf("DefaultReadStateForRuntime: %v", err)
	}
	if st == nil {
		t.Fatal("DefaultReadStateForRuntime returned nil state")
	}
}

func TestDefaultReadStateForRuntimeNilConfig(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	rt := globals.Runtime{}
	st, err := DefaultReadStateForRuntime(rt)
	if err != nil {
		t.Fatalf("DefaultReadStateForRuntime nil config: %v", err)
	}
	if st == nil {
		t.Fatal("DefaultReadStateForRuntime nil config returned nil state")
	}
}

func TestDefaultReadStateMethod(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	rt := globals.Runtime{Config: config.Defaults()}
	c := New(
		StartSpec,
		rt,
		false, false, false, &bytes.Buffer{},
		func(_, _ string) bool { return false },
		nil,
	)
	st, err := c.DefaultReadState()
	if err != nil {
		t.Fatalf("DefaultReadState: %v", err)
	}
	if st == nil {
		t.Fatal("DefaultReadState returned nil state")
	}
}

func TestDefaultWriteStateStandalone(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	st := fabricastate.NewState("123456789012", "us-east-1")
	err := DefaultWriteState(st)
	if err != nil {
		t.Fatalf("DefaultWriteState: %v", err)
	}
}

func TestDefaultWriteStateMethod(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	rt := globals.Runtime{Config: config.Defaults()}
	c := New(
		StartSpec,
		rt,
		false, false, false, &bytes.Buffer{},
		func(_, _ string) bool { return false },
		nil,
	)
	st := fabricastate.NewState("123456789012", "us-east-1")
	err := c.DefaultWriteState(st)
	if err != nil {
		t.Fatalf("DefaultWriteState method: %v", err)
	}
}

func TestDefaultExecuteActionNilProvider(t *testing.T) {
	rt := globals.Runtime{}
	fn := DefaultExecuteAction(rt, StartVerb)
	err := fn(context.Background(), "i-123")
	if err == nil {
		t.Fatal("expected error for nil provider")
	}
	if !strings.Contains(err.Error(), "no provider configured") {
		t.Errorf("err: %v", err)
	}
}

func TestDefaultExecuteActionUnsupportedProvider(t *testing.T) {
	rt := globals.Runtime{Provider: &fakeNoEC2Manager{}}
	fn := DefaultExecuteAction(rt, StartVerb)
	err := fn(context.Background(), "i-123")
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
	if !strings.Contains(err.Error(), "does not support EC2") {
		t.Errorf("err: %v", err)
	}
}

func TestDefaultExecuteActionUnknownVerb(t *testing.T) {
	rt := globals.Runtime{Provider: &fakeEC2Manager{}}
	fn := DefaultExecuteAction(rt, "unknown")
	err := fn(context.Background(), "i-123")
	if err == nil {
		t.Fatal("expected error for unknown verb")
	}
	if !strings.Contains(err.Error(), "unknown action verb") {
		t.Errorf("err: %v", err)
	}
}

func TestDefaultExecuteActionStartVerb(t *testing.T) {
	rt := globals.Runtime{Provider: &fakeEC2Manager{}}
	fn := DefaultExecuteAction(rt, StartVerb)
	err := fn(context.Background(), "i-abc")
	if err != nil {
		t.Fatalf("DefaultExecuteAction start: %v", err)
	}
}

func TestDefaultExecuteActionStopVerb(t *testing.T) {
	rt := globals.Runtime{Provider: &fakeEC2Manager{}}
	fn := DefaultExecuteAction(rt, StopVerb)
	err := fn(context.Background(), "i-abc")
	if err != nil {
		t.Fatalf("DefaultExecuteAction stop: %v", err)
	}
}

// fakeEC2Manager implements Provider + EC2InstanceManager for testing DefaultExecuteAction.
type fakeEC2Manager struct{}

func (f *fakeEC2Manager) Name() string { return "fake" }
func (f *fakeEC2Manager) Identity(ctx context.Context) (string, string, string, error) {
	return "12345", "arn", "us-east-1", nil
}
func (f *fakeEC2Manager) Resources() fabricac.ResourceClient                 { return nil }
func (f *fakeEC2Manager) StopInstance(ctx context.Context, id string) error  { return nil }
func (f *fakeEC2Manager) StartInstance(ctx context.Context, id string) error { return nil }

// fakeNoEC2Manager is a provider that does NOT implement EC2InstanceManager.
type fakeNoEC2Manager struct{}

func (f *fakeNoEC2Manager) Name() string { return "fake" }
func (f *fakeNoEC2Manager) Identity(ctx context.Context) (string, string, string, error) {
	return "12345", "arn", "us-east-1", nil
}
func (f *fakeNoEC2Manager) Resources() fabricac.ResourceClient { return nil }
