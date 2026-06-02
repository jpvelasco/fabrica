package terminate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func newTestCommand(out *bytes.Buffer, st *fabricastate.State, deleteErr map[string]error) command {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	c := command{
		runtime: globals.Runtime{Config: cfg, Provider: nil},
		out:     out,
		confirm: func(_, _ string) bool { return true },
	}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	c.writeState = func(_ *fabricastate.State) error { return nil }
	fake := &fakeResourceClient{deleteErr: deleteErr}
	c.deleteResource = fake.Delete
	c.getResource = fake.Get
	return c
}

func workstationState(status string, withInstance bool) *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	resources := []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-ws123"},
	}
	if withInstance {
		resources = append(resources, fabricastate.ModuleResource{
			TypeName:   "AWS::EC2::Instance",
			Identifier: "i-ws123",
		})
	}
	st.UpsertModule("workstation", "ami-test", status, resources)
	return st
}

func TestTerminateNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, st, nil)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "not provisioned")
}

func TestTerminateDryRunNoDeleteCalls(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready", true)
	deleteCalled := false
	c := newTestCommand(&out, st, nil)
	c.dryRun = true
	c.deleteResource = func(_ context.Context, _ *cloud.Resource) error {
		deleteCalled = true
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if deleteCalled {
		t.Error("dry-run made delete calls")
	}
	assertContains(t, out.String(), "dry run")
}

func TestTerminateDryRunOutputFields(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready", true)
	c := newTestCommand(&out, st, nil)
	c.dryRun = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	for _, want := range []string{"i-ws123", "sg-ws123", "without --dry-run"} {
		assertContains(t, got, want)
	}
}

func TestTerminateDeleteOrderInstanceBeforeSG(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready", true)
	var deletedTypes []string
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.deleteResource = func(_ context.Context, r *cloud.Resource) error {
		deletedTypes = append(deletedTypes, r.TypeName)
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(deletedTypes) != 2 {
		t.Fatalf("expected 2 deletes, got %d: %v", len(deletedTypes), deletedTypes)
	}
	if deletedTypes[0] != "AWS::EC2::Instance" {
		t.Errorf("first deleted = %q, want AWS::EC2::Instance", deletedTypes[0])
	}
	if deletedTypes[1] != "AWS::EC2::SecurityGroup" {
		t.Errorf("second deleted = %q, want AWS::EC2::SecurityGroup", deletedTypes[1])
	}
}

func TestTerminateHappyPathModuleRemovedFromState(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready", true)
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
	if m := lastState.GetModule("workstation"); m != nil {
		t.Error("workstation module must be removed after successful terminate")
	}
}

func TestTerminatePartialStateWrittenAfterEachDelete(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready", true)

	type snap struct {
		moduleExists  bool
		resourceTypes []string
	}
	var snaps []snap
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.writeState = func(s *fabricastate.State) error {
		m := s.GetModule("workstation")
		sn := snap{moduleExists: m != nil}
		if m != nil {
			for _, r := range m.Resources {
				sn.resourceTypes = append(sn.resourceTypes, r.TypeName)
			}
		}
		snaps = append(snaps, sn)
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(snaps) < 3 {
		t.Fatalf("expected >=3 state writes, got %d", len(snaps))
	}
	s0 := snaps[0]
	if !s0.moduleExists {
		t.Fatal("module should still exist after first delete")
	}
	hasType := func(types []string, typ string) bool {
		for _, tt := range types {
			if tt == typ {
				return true
			}
		}
		return false
	}
	if hasType(s0.resourceTypes, "AWS::EC2::Instance") {
		t.Error("instance should be removed from state after first delete")
	}
	if !hasType(s0.resourceTypes, "AWS::EC2::SecurityGroup") {
		t.Error("SG should still be in state after first delete")
	}
	last := snaps[len(snaps)-1]
	if last.moduleExists {
		t.Error("module must be absent in final state write")
	}
}

func TestTerminateConfirmationRejected(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready", true)
	deleteCalled := false
	c := newTestCommand(&out, st, nil)
	c.confirm = func(_, _ string) bool { return false }
	c.deleteResource = func(_ context.Context, _ *cloud.Resource) error {
		deleteCalled = true
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if deleteCalled {
		t.Error("delete was called after confirmation rejected")
	}
	assertContains(t, out.String(), "Cancelled")
}

func TestTerminateConfirmationPhrase(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready", true)
	var capturedPhrase string
	c := newTestCommand(&out, st, nil)
	c.confirm = func(_, phrase string) bool {
		capturedPhrase = phrase
		return false
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if capturedPhrase != "terminate workstation 123456789012" {
		t.Errorf("phrase = %q, want %q", capturedPhrase, "terminate workstation 123456789012")
	}
}

func TestTerminateNilProviderErrors(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready", true)
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.deleteResource = nil

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error with nil deleteResource")
	}
	assertContains(t, err.Error(), "no provider configured")
}

func TestTerminateReadStateError(t *testing.T) {
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

func TestTerminateWriteStateErrorSurfacedAsWarning(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready", true)
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.writeState = func(_ *fabricastate.State) error {
		return errors.New("disk full")
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("writeState failure must not abort terminate: %v", err)
	}
	assertContains(t, out.String(), "Warning")
}

func TestTerminateNotFoundOnDeleteTreatedAsSuccess(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready", true)
	c := newTestCommand(&out, st, map[string]error{
		"AWS::EC2::Instance": cloud.ErrResourceNotFound,
	})
	c.assumeYes = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("ErrResourceNotFound must not abort terminate: %v", err)
	}
	if m := st.GetModule("workstation"); m != nil {
		t.Error("module must be removed after terminate completes")
	}
}

func TestTerminateInstanceTransitionalStateErrors(t *testing.T) {
	for _, state := range []string{"stopping", "shutting-down"} {
		t.Run(state, func(t *testing.T) {
			var out bytes.Buffer
			st := workstationState("ready", true)
			c := newTestCommand(&out, st, nil)
			c.assumeYes = true
			actualState := []byte(`{"State":{"Name":"` + state + `"}}`)
			fake := &fakeResourceClient{
				getResponse: map[string][]byte{"AWS::EC2::Instance": actualState},
			}
			c.getResource = fake.Get
			deleteCalled := false
			c.deleteResource = func(_ context.Context, _ *cloud.Resource) error {
				deleteCalled = true
				return nil
			}

			err := c.run(context.Background())
			if err == nil {
				t.Fatalf("expected error for transitional state %q", state)
			}
			assertContains(t, err.Error(), state)
			if deleteCalled {
				t.Error("delete must not be called in transitional state")
			}
		})
	}
}

func TestTerminateInstanceTerminatedTreatedAsGone(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready", true)
	actualState := []byte(`{"State":{"Name":"terminated"}}`)
	fake := &fakeResourceClient{
		getResponse: map[string][]byte{"AWS::EC2::Instance": actualState},
	}
	var deletedTypes []string
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.getResource = fake.Get
	c.deleteResource = func(_ context.Context, r *cloud.Resource) error {
		deletedTypes = append(deletedTypes, r.TypeName)
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("terminated instance must not error: %v", err)
	}
	for _, typ := range deletedTypes {
		if typ == "AWS::EC2::Instance" {
			t.Error("delete must not be called for terminated instance")
		}
	}
}

func TestTerminateJSONNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, st, nil)
	c.jsonOut = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var result TerminateOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if len(result.Destroyed) != 0 {
		t.Errorf("destroyed = %v, want empty", result.Destroyed)
	}
}

func TestTerminateJSONHappyPath(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready", true)
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.jsonOut = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var result TerminateOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if result.DryRun {
		t.Error("dryRun must be false")
	}
	if len(result.Destroyed) != 2 {
		t.Errorf("expected 2 destroyed, got %d: %v", len(result.Destroyed), result.Destroyed)
	}
}

func TestTerminateJSONDryRun(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready", true)
	c := newTestCommand(&out, st, nil)
	c.dryRun = true
	c.jsonOut = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var result TerminateOutput
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if !result.DryRun {
		t.Error("dryRun must be true")
	}
	if len(result.Destroyed) != 2 {
		t.Errorf("expected 2 in destroyed list for dry run, got %d", len(result.Destroyed))
	}
}

func TestTerminateAssumeYesSkipsPrompt(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready", true)
	confirmCalled := false
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.confirm = func(_, _ string) bool {
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

func TestTerminateSGOnlyNoInstance(t *testing.T) {
	var out bytes.Buffer
	st := workstationState("ready", false)
	var deletedTypes []string
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.deleteResource = func(_ context.Context, r *cloud.Resource) error {
		deletedTypes = append(deletedTypes, r.TypeName)
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(deletedTypes) != 1 || deletedTypes[0] != "AWS::EC2::SecurityGroup" {
		t.Errorf("expected 1 SG delete, got: %v", deletedTypes)
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

type fakeResourceClient struct {
	deleteErr   map[string]error
	getErr      map[string]error
	getResponse map[string][]byte
	deleted     []string
}

func (f *fakeResourceClient) Delete(_ context.Context, r *cloud.Resource) error {
	if err, ok := f.deleteErr[r.TypeName]; ok {
		return err
	}
	f.deleted = append(f.deleted, r.Identifier)
	return nil
}

func (f *fakeResourceClient) Get(_ context.Context, r *cloud.Resource) error {
	if err, ok := f.getErr[r.TypeName]; ok {
		return err
	}
	if data, ok := f.getResponse[r.TypeName]; ok {
		r.ActualState = data
	}
	return nil
}

func (f *fakeResourceClient) Create(_ context.Context, _ *cloud.Resource) error { return nil }
func (f *fakeResourceClient) Update(_ context.Context, _ *cloud.Resource) error { return nil }
func (f *fakeResourceClient) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}
