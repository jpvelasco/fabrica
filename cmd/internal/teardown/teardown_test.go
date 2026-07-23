package teardown

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// testSpec mirrors the perforce destroy spec — the engine is module-agnostic,
// so one representative spec exercises all shared logic.
var testSpec = Spec{
	ModuleName:     "perforce",
	Verb:           "destroy",
	VersionLabel:   "Version",
	Title:          "Perforce Helix Core",
	NotProvisioned: "Perforce is not provisioned. Nothing to destroy.",
	PlanHeader:     "Perforce Helix Core — destroy plan",
	DryRunHeader:   "Perforce Helix Core (destroy dry run)",
	Irreversible:   "IRREVERSIBLE: This will permanently delete the Perforce server and its data.",
	SuccessMessage: "Perforce Helix Core destroyed.",
}

// newTestCommand builds a teardown command with injected state and fake delete seam.
func newTestCommand(out *bytes.Buffer, st *fabricastate.State, deleteErr map[string]error) Command {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	c := Command{
		Spec:    testSpec,
		Runtime: globals.Runtime{Config: cfg, Provider: nil},
		Out:     out,
		Confirm: func(_, _ string) bool { return true },
	}
	c.ReadState = func() (*fabricastate.State, error) { return st, nil }
	c.WriteState = func(s *fabricastate.State) error { return nil }
	fake := &fakeResourceClient{deleteErr: deleteErr}
	c.DeleteResource = fake.Delete
	c.GetResource = fake.Get
	return c
}

func moduleState(status string, withInstance bool) *fabricastate.State {
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

func TestRunNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, st, nil)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	assertContains(t, out.String(), "not provisioned")
}

func TestRunDryRunNoDeleteCalls(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)
	deleteCalled := false
	c := newTestCommand(&out, st, nil)
	c.DryRun = true
	c.DeleteResource = func(_ context.Context, _ *cloud.Resource) error {
		deleteCalled = true
		return nil
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if deleteCalled {
		t.Error("dry-run made delete calls")
	}
	assertContains(t, out.String(), "dry run")
}

func TestRunDryRunOutputFields(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)
	c := newTestCommand(&out, st, nil)
	c.DryRun = true

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"i-abc123",
		"sg-abc123",
		"AWS::EC2::Instance",
		"AWS::EC2::SecurityGroup",
		"without --dry-run",
	} {
		assertContains(t, got, want)
	}
}

func TestRunDeleteOrderInstanceBeforeSG(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)
	var deletedTypes []string
	c := newTestCommand(&out, st, nil)
	c.AssumeYes = true
	c.DeleteResource = func(_ context.Context, r *cloud.Resource) error {
		deletedTypes = append(deletedTypes, r.TypeName)
		return nil
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
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

func TestRunHappyPathModuleRemovedFromState(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)
	var lastState *fabricastate.State
	c := newTestCommand(&out, st, nil)
	c.AssumeYes = true
	c.WriteState = func(s *fabricastate.State) error {
		cp := *s
		lastState = &cp
		return nil
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if lastState == nil {
		t.Fatal("state was never written")
	}
	if m := lastState.GetModule("perforce"); m != nil {
		t.Error("module must be removed from state after successful destroy")
	}
}

func TestRunPartialStateWrittenAfterEachDelete(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)

	type snapshot struct {
		moduleExists  bool
		resourceTypes []string
	}
	var snapshots []snapshot
	c := newTestCommand(&out, st, nil)
	c.AssumeYes = true
	c.WriteState = func(s *fabricastate.State) error {
		m := s.GetModule("perforce")
		snap := snapshot{moduleExists: m != nil}
		if m != nil {
			for _, r := range m.Resources {
				snap.resourceTypes = append(snap.resourceTypes, r.TypeName)
			}
		}
		snapshots = append(snapshots, snap)
		return nil
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(snapshots) < 3 {
		t.Fatalf("expected at least 3 state writes, got %d", len(snapshots))
	}

	s0 := snapshots[0]
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

	lastSnap := snapshots[len(snapshots)-1]
	if lastSnap.moduleExists {
		t.Error("module must be absent in final state write")
	}
}

func TestRunInstanceFailureLeavesStateIntact(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)
	c := newTestCommand(&out, st, map[string]error{
		"AWS::EC2::Instance": errors.New("instance termination failed"),
	})
	c.AssumeYes = true

	err := c.Run(context.Background())
	if err == nil {
		t.Fatal("expected error on instance delete failure")
	}
	assertContains(t, err.Error(), "deleting AWS::EC2::Instance")
	if m := st.GetModule("perforce"); m == nil {
		t.Error("module must remain in state after failed delete")
	}
}

func TestRunSGFailureAfterInstanceSuccess(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)
	var lastState *fabricastate.State
	c := newTestCommand(&out, st, map[string]error{
		"AWS::EC2::SecurityGroup": errors.New("sg in use"),
	})
	c.AssumeYes = true
	c.WriteState = func(s *fabricastate.State) error {
		cp := *s
		lastState = &cp
		return nil
	}

	err := c.Run(context.Background())
	if err == nil {
		t.Fatal("expected error on SG delete failure")
	}
	assertContains(t, err.Error(), "deleting AWS::EC2::SecurityGroup")

	if lastState == nil {
		t.Fatal("state was never written")
	}
	m := lastState.GetModule("perforce")
	if m == nil {
		t.Fatal("module must remain in state after partial failure")
	}
	_, hasInstance := getResource(m, "AWS::EC2::Instance")
	if hasInstance {
		t.Error("instance should be removed from state since it was deleted successfully")
	}
	_, hasSG := getResource(m, "AWS::EC2::SecurityGroup")
	if !hasSG {
		t.Error("SG should remain in state since its deletion failed")
	}
}

func TestRunSGOnlyNoInstance(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", false) // SG only
	var deletedTypes []string
	c := newTestCommand(&out, st, nil)
	c.AssumeYes = true
	c.DeleteResource = func(_ context.Context, r *cloud.Resource) error {
		deletedTypes = append(deletedTypes, r.TypeName)
		return nil
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(deletedTypes) != 1 || deletedTypes[0] != "AWS::EC2::SecurityGroup" {
		t.Errorf("expected 1 SG delete, got: %v", deletedTypes)
	}
}

func TestRunConfirmationRejectedNoDeleteCalls(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)
	deleteCalled := false
	c := newTestCommand(&out, st, nil)
	c.Confirm = func(_, _ string) bool { return false }
	c.DeleteResource = func(_ context.Context, _ *cloud.Resource) error {
		deleteCalled = true
		return nil
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if deleteCalled {
		t.Error("delete was called after confirmation rejected")
	}
	assertContains(t, out.String(), "Cancelled")
}

func TestRunConfirmationPhrase(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)
	var capturedPhrase string
	c := newTestCommand(&out, st, nil)
	c.Confirm = func(_, phrase string) bool {
		capturedPhrase = phrase
		return false
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := "destroy perforce 123456789012"
	if capturedPhrase != want {
		t.Errorf("phrase = %q, want %q", capturedPhrase, want)
	}
	assertContains(t, out.String(), want)
}

func TestRunReadStateError(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, nil, nil)
	c.ReadState = func() (*fabricastate.State, error) {
		return nil, errors.New("disk read failure")
	}
	deleteCalled := false
	c.DeleteResource = func(_ context.Context, _ *cloud.Resource) error {
		deleteCalled = true
		return nil
	}

	err := c.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when ReadState fails")
	}
	assertContains(t, err.Error(), "reading state")
	if deleteCalled {
		t.Error("delete was called after ReadState failure")
	}
}

func TestRunNilProviderErrors(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)
	c := newTestCommand(&out, st, nil)
	c.AssumeYes = true
	c.DeleteResource = nil // simulate nil provider

	err := c.Run(context.Background())
	if err == nil {
		t.Fatal("expected error with nil DeleteResource")
	}
	assertContains(t, err.Error(), "no provider configured")
}

func TestRunWriteStateErrorSurfacedAsWarning(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)
	c := newTestCommand(&out, st, nil)
	c.AssumeYes = true
	c.WriteState = func(_ *fabricastate.State) error {
		return errors.New("disk full")
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("WriteState failure must not abort destroy: %v", err)
	}
	assertContains(t, out.String(), "Warning")
	assertContains(t, out.String(), "disk full")
}

func TestRunJSONNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, st, nil)
	c.JSONOut = true

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var result Output
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if len(result.Destroyed) != 0 {
		t.Errorf("destroyed = %v, want empty", result.Destroyed)
	}
}

func TestRunJSONDryRun(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)
	c := newTestCommand(&out, st, nil)
	c.DryRun = true
	c.JSONOut = true

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var result Output
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if !result.DryRun {
		t.Error("dryRun field must be true")
	}
	if len(result.Destroyed) != 2 {
		t.Errorf("expected 2 resources in dry-run output, got %d: %v", len(result.Destroyed), result.Destroyed)
	}
}

func TestRunJSONHappyPath(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)
	c := newTestCommand(&out, st, nil)
	c.AssumeYes = true
	c.JSONOut = true

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var result Output
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if result.DryRun {
		t.Error("dryRun must be false on real destroy")
	}
	if len(result.Destroyed) != 2 {
		t.Errorf("expected 2 destroyed, got %d: %v", len(result.Destroyed), result.Destroyed)
	}
}

func TestRunAssumeYesSkipsPrompt(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)
	confirmCalled := false
	c := newTestCommand(&out, st, nil)
	c.AssumeYes = true
	c.Confirm = func(_, _ string) bool {
		confirmCalled = true
		return true
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if confirmCalled {
		t.Error("confirm must not be called when --yes is set")
	}
}

func TestResourcesToDeleteOrder(t *testing.T) {
	cases := []struct {
		name      string
		resources []fabricastate.ModuleResource
		wantTypes []string
	}{
		{
			"instance and sg",
			[]fabricastate.ModuleResource{
				{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-1"},
				{TypeName: "AWS::EC2::Instance", Identifier: "i-1"},
			},
			[]string{"AWS::EC2::Instance", "AWS::EC2::SecurityGroup"},
		},
		{
			"sg only",
			[]fabricastate.ModuleResource{
				{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-1"},
			},
			[]string{"AWS::EC2::SecurityGroup"},
		},
		{
			"instance only",
			[]fabricastate.ModuleResource{
				{TypeName: "AWS::EC2::Instance", Identifier: "i-1"},
			},
			[]string{"AWS::EC2::Instance"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &fabricastate.ModuleState{Resources: tc.resources}
			got := resourcesToDelete(m)
			if len(got) != len(tc.wantTypes) {
				t.Fatalf("got %d resources, want %d", len(got), len(tc.wantTypes))
			}
			for i, want := range tc.wantTypes {
				if got[i].TypeName != want {
					t.Errorf("[%d] TypeName = %q, want %q", i, got[i].TypeName, want)
				}
			}
		})
	}
}

func TestRunNotFoundOnDeleteTreatedAsSuccess(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)
	c := newTestCommand(&out, st, map[string]error{
		"AWS::EC2::Instance": cloud.ErrResourceNotFound,
	})
	c.AssumeYes = true

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("ErrResourceNotFound must not abort destroy: %v", err)
	}
	if m := st.GetModule("perforce"); m != nil {
		t.Error("module must be removed from state after destroy completes")
	}
}

func TestRunNotFoundBothResourcesSucceeds(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)
	c := newTestCommand(&out, st, map[string]error{
		"AWS::EC2::Instance":      cloud.ErrResourceNotFound,
		"AWS::EC2::SecurityGroup": cloud.ErrResourceNotFound,
	})
	c.AssumeYes = true

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("all-not-found must complete without error: %v", err)
	}
	if m := st.GetModule("perforce"); m != nil {
		t.Error("module must be removed from state")
	}
}

func TestRunInstanceTransitionalStateReturnsError(t *testing.T) {
	for _, state := range []string{"stopping", "shutting-down"} {
		t.Run(state, func(t *testing.T) {
			var out bytes.Buffer
			st := moduleState("provisioning", true)
			c := newTestCommand(&out, st, nil)
			c.AssumeYes = true
			actualState := []byte(`{"State":{"Name":"` + state + `"}}`)
			deleteCalled := false
			fake := &fakeResourceClient{
				getResponse: map[string][]byte{"AWS::EC2::Instance": actualState},
			}
			c.GetResource = fake.Get
			c.DeleteResource = func(_ context.Context, _ *cloud.Resource) error {
				deleteCalled = true
				return nil
			}

			err := c.Run(context.Background())
			if err == nil {
				t.Fatalf("expected error for transitional state %q", state)
			}
			assertContains(t, err.Error(), state)
			assertContains(t, err.Error(), "transitional state")
			if deleteCalled {
				t.Error("delete must not be called when instance is in transitional state")
			}
		})
	}
}

func TestRunInstanceTerminatedTreatedAsGone(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)
	actualState := []byte(`{"State":{"Name":"terminated"}}`)
	fake := &fakeResourceClient{
		getResponse: map[string][]byte{"AWS::EC2::Instance": actualState},
	}
	deletedTypes := []string{}
	c := newTestCommand(&out, st, nil)
	c.AssumeYes = true
	c.GetResource = fake.Get
	c.DeleteResource = func(_ context.Context, r *cloud.Resource) error {
		deletedTypes = append(deletedTypes, r.TypeName)
		return nil
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("terminated instance must not produce error: %v", err)
	}
	for _, typ := range deletedTypes {
		if typ == "AWS::EC2::Instance" {
			t.Error("delete must not be called for terminated instance")
		}
	}
	found := false
	for _, typ := range deletedTypes {
		if typ == "AWS::EC2::SecurityGroup" {
			found = true
		}
	}
	if !found {
		t.Error("SG should still be deleted even when instance was already terminated")
	}
}

func TestRunGetResourceNotFoundBeforeDeleteTreatedAsGone(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)
	fake := &fakeResourceClient{
		getErr: map[string]error{"AWS::EC2::Instance": cloud.ErrResourceNotFound},
	}
	deletedTypes := []string{}
	c := newTestCommand(&out, st, nil)
	c.AssumeYes = true
	c.GetResource = fake.Get
	c.DeleteResource = func(_ context.Context, r *cloud.Resource) error {
		deletedTypes = append(deletedTypes, r.TypeName)
		return nil
	}

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Get not-found before delete must not produce error: %v", err)
	}
	for _, typ := range deletedTypes {
		if typ == "AWS::EC2::Instance" {
			t.Error("delete must not be called when Get returns ErrResourceNotFound")
		}
	}
}

func TestRunGetResourceErrorPropagates(t *testing.T) {
	var out bytes.Buffer
	st := moduleState("provisioning", true)
	fake := &fakeResourceClient{
		getErr: map[string]error{"AWS::EC2::Instance": errors.New("network timeout")},
	}
	c := newTestCommand(&out, st, nil)
	c.AssumeYes = true
	c.GetResource = fake.Get

	err := c.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when GetResource fails")
	}
	assertContains(t, err.Error(), "network timeout")
}

// TestSpecVerbInConfirmPhrase confirms the Spec's verb/module compose the phrase,
// covering the terminate variant (verb "terminate") not exercised by testSpec.
func TestSpecVerbInConfirmPhrase(t *testing.T) {
	c := Command{Spec: Spec{ModuleName: "workstation", Verb: "terminate"}}
	if got := c.confirmPhrase("acct"); got != "terminate workstation acct" {
		t.Errorf("confirmPhrase = %q, want %q", got, "terminate workstation acct")
	}
}

// ---- helpers ----

func getResource(m *fabricastate.ModuleState, typeName string) (fabricastate.ModuleResource, bool) {
	for _, r := range m.Resources {
		if r.TypeName == typeName {
			return r, true
		}
	}
	return fabricastate.ModuleResource{}, false
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

// fakeResourceClient tracks delete calls and returns configured errors.
type fakeResourceClient struct {
	deleteErr   map[string]error
	getErr      map[string]error
	getResponse map[string][]byte // TypeName → ActualState JSON to inject
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

func TestResourceOrderCustomHook(t *testing.T) {
	called := false
	spec := testSpec
	spec.ResourceOrder = func(m *fabricastate.ModuleState) []cloud.Resource {
		called = true
		// reverse the resources to prove the hook drives ordering
		out := make([]cloud.Resource, 0, len(m.Resources))
		for i := len(m.Resources) - 1; i >= 0; i-- {
			out = append(out, cloud.Resource{TypeName: m.Resources[i].TypeName, Identifier: m.Resources[i].Identifier})
		}
		return out
	}
	m := &fabricastate.ModuleState{
		Name: "perforce",
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-1"},
			{TypeName: "AWS::GameLift::Build", Identifier: "build-1"},
		},
	}
	got := resourcesToDelete2(spec, m)
	if !called {
		t.Fatal("ResourceOrder hook not invoked")
	}
	if len(got) != 2 || got[0].Identifier != "build-1" {
		t.Fatalf("custom order not applied: %+v", got)
	}
}

func TestResourceOrderNilDefault(t *testing.T) {
	m := &fabricastate.ModuleState{
		Name: "perforce",
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-1"},
			{TypeName: "AWS::EC2::Instance", Identifier: "i-1"},
		},
	}
	got := resourcesToDelete2(testSpec, m) // testSpec.ResourceOrder is nil
	if len(got) != 2 || got[0].TypeName != "AWS::EC2::Instance" {
		t.Fatalf("default EC2->SG order broken: %+v", got)
	}
}

func TestRunSkipConfirmBypassesConfirmation(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2024.1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-1"},
	})

	var deleted []string
	var written bool
	c := Command{
		Spec:        Spec{ModuleName: "perforce", Verb: "destroy", Title: "Perforce", SuccessMessage: "done"},
		Runtime:     globals.Runtime{},
		SkipConfirm: true,
		Out:         &bytes.Buffer{},
		Confirm: func(string, string) bool {
			t.Fatal("Confirm must not be called when SkipConfirm is true")
			return false
		},
		ReadState:  func() (*fabricastate.State, error) { return st, nil },
		WriteState: func(*fabricastate.State) error { written = true; return nil },
		DeleteResource: func(_ context.Context, r *cloud.Resource) error {
			deleted = append(deleted, r.Identifier)
			return nil
		},
	}
	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "sg-1" {
		t.Fatalf("expected sg-1 deleted, got %v", deleted)
	}
	if !written {
		t.Fatal("expected state to be written")
	}
}

func TestRemoveModule(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2024.1", "ready", nil)
	st.UpsertModule("horde", "2024.1", "ready", nil)

	RemoveModule(st, "perforce")

	if st.GetModule("perforce") != nil {
		t.Error("perforce should be removed")
	}
	if st.GetModule("horde") == nil {
		t.Error("horde should still exist")
	}
}

func TestRemoveModuleNonexistent(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "2024.1", "ready", nil)

	RemoveModule(st, "nonexistent")

	if st.GetModule("horde") == nil {
		t.Error("horde should still exist")
	}
}

type fakeProviderWithRC struct {
	rc *fakeResourceClient
}

func (f *fakeProviderWithRC) Name() string { return "test" }
func (f *fakeProviderWithRC) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "", "us-east-1", nil
}
func (f *fakeProviderWithRC) Resources() cloud.ResourceClient { return f.rc }

func TestWireProvider(t *testing.T) {
	fakeRC := &fakeResourceClient{}
	rt := globals.Runtime{Provider: &fakeProviderWithRC{rc: fakeRC}}

	tc := &Command{}
	WireProvider(tc, rt)

	if tc.DeleteResource == nil {
		t.Error("DeleteResource should be set")
	}
	if tc.GetResource == nil {
		t.Error("GetResource should be set")
	}
}

func TestWireProviderNilProvider(t *testing.T) {
	rt := globals.Runtime{Provider: nil}

	tc := &Command{}
	WireProvider(tc, rt)

	if tc.DeleteResource != nil {
		t.Error("DeleteResource should remain nil")
	}
}

// TestNewTeardownWiring verifies NewTeardown returns a Command with correct wiring.
func TestNewTeardownWiring(t *testing.T) {
	var out bytes.Buffer
	rt := globals.Runtime{Config: config.Defaults(), Provider: &testutil.CobraFakeProvider{}}
	tc := NewTeardown(testSpec, rt, &out)

	if !tc.SkipConfirm || !tc.AssumeYes {
		t.Fatalf("SkipConfirm/AssumeYes must be true; got SkipConfirm=%v, AssumeYes=%v", tc.SkipConfirm, tc.AssumeYes)
	}
	if tc.ReadState == nil {
		t.Fatal("ReadState must be wired")
	}
	if tc.WriteState == nil {
		t.Fatal("WriteState must be wired")
	}
	if tc.DeleteResource == nil {
		t.Fatal("DeleteResource must be wired when provider is non-nil")
	}
	if tc.GetResource == nil {
		t.Fatal("GetResource must be wired when provider is non-nil")
	}
	if tc.Confirm == nil {
		t.Fatal("Confirm must be wired")
	}
	if tc.Spec.ModuleName != "perforce" {
		t.Errorf("Spec.ModuleName = %q, want perforce", tc.Spec.ModuleName)
	}
}

// TestNewTeardownNilProvider verifies NewTeardown handles nil provider gracefully.
func TestNewTeardownNilProvider(t *testing.T) {
	var out bytes.Buffer
	rt := globals.Runtime{Config: config.Defaults(), Provider: nil}
	tc := NewTeardown(testSpec, rt, &out)

	if !tc.SkipConfirm || !tc.AssumeYes {
		t.Fatal("SkipConfirm/AssumeYes must be true even with nil provider")
	}
	if tc.ReadState == nil || tc.WriteState == nil {
		t.Fatal("ReadState and WriteState must always be wired")
	}
	if tc.DeleteResource != nil || tc.GetResource != nil {
		t.Fatal("DeleteResource/GetResource must be nil when provider is nil")
	}
}
