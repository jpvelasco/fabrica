package destroy

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

// newTestCommand builds a destroy command with injected state and fake delete seam.
func newTestCommand(out *bytes.Buffer, st *fabricastate.State, deleteErr map[string]error) command {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	c := command{
		runtime: globals.Runtime{Config: cfg, Provider: nil},
		out:     out,
		confirm: func(_, _ string) bool { return true },
	}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	var written []*fabricastate.State
	c.writeState = func(s *fabricastate.State) error {
		copy := *s
		written = append(written, &copy)
		return nil
	}
	fake := &fakeResourceClient{deleteErr: deleteErr}
	c.deleteResource = fake.Delete
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

// TestDestroyNotProvisioned verifies clean exit when no module is in state.
func TestDestroyNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, st, nil)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "not provisioned")
}

// TestDestroyDryRunNoDeleteCalls verifies --dry-run makes zero provider calls.
func TestDestroyDryRunNoDeleteCalls(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
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

// TestDestroyDryRunOutputFields verifies plan content in dry-run output.
func TestDestroyDryRunOutputFields(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	c := newTestCommand(&out, st, nil)
	c.dryRun = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
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

// TestDestroyDeleteOrderInstanceBeforeSG verifies Instance is deleted before SG.
func TestDestroyDeleteOrderInstanceBeforeSG(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
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

// TestDestroyHappyPathModuleRemovedFromState verifies module is absent after full destroy.
func TestDestroyHappyPathModuleRemovedFromState(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	var lastState *fabricastate.State
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.writeState = func(s *fabricastate.State) error {
		copy := *s
		lastState = &copy
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if lastState == nil {
		t.Fatal("state was never written")
	}
	if m := lastState.GetModule("perforce"); m != nil {
		t.Error("perforce module must be removed from state after successful destroy")
	}
}

// TestDestroyPartialStateWrittenAfterEachDelete verifies incremental state cleanup.
func TestDestroyPartialStateWrittenAfterEachDelete(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)

	type snapshot struct {
		moduleExists  bool
		resourceTypes []string
	}
	var snapshots []snapshot
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.writeState = func(s *fabricastate.State) error {
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

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	// Should have written: after instance delete, after SG delete, after module removal = 3 writes.
	if len(snapshots) < 3 {
		t.Fatalf("expected at least 3 state writes, got %d", len(snapshots))
	}

	// After first write (instance deleted): module still exists, instance removed, SG present.
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

	// Final write: module removed entirely.
	lastSnap := snapshots[len(snapshots)-1]
	if lastSnap.moduleExists {
		t.Error("module must be absent in final state write")
	}
}

// TestDestroyInstanceFailureLeavesStateIntact verifies state preserved on instance delete error.
func TestDestroyInstanceFailureLeavesStateIntact(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	c := newTestCommand(&out, st, map[string]error{
		"AWS::EC2::Instance": errors.New("instance termination failed"),
	})
	c.assumeYes = true

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error on instance delete failure")
	}
	assertContains(t, err.Error(), "deleting AWS::EC2::Instance")
	// Module must still be in state.
	if m := st.GetModule("perforce"); m == nil {
		t.Error("module must remain in state after failed delete")
	}
}

// TestDestroySGFailureAfterInstanceSuccess verifies partial state: instance gone, SG still in error.
func TestDestroySGFailureAfterInstanceSuccess(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	var lastState *fabricastate.State
	c := newTestCommand(&out, st, map[string]error{
		"AWS::EC2::SecurityGroup": errors.New("sg in use"),
	})
	c.assumeYes = true
	c.writeState = func(s *fabricastate.State) error {
		cp := *s
		lastState = &cp
		return nil
	}

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error on SG delete failure")
	}
	assertContains(t, err.Error(), "deleting AWS::EC2::SecurityGroup")

	// Instance should have been removed from state (it was deleted successfully).
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

// TestDestroySGOnlyNoInstance verifies partial state (only SG) is handled gracefully.
func TestDestroySGOnlyNoInstance(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", false) // SG only
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

// TestDestroyConfirmationRejectedNoDeleteCalls verifies cancellation skips delete.
func TestDestroyConfirmationRejectedNoDeleteCalls(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
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

// TestDestroyConfirmationPhrase verifies the exact phrase shown to the user.
func TestDestroyConfirmationPhrase(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	var capturedPhrase string
	c := newTestCommand(&out, st, nil)
	c.confirm = func(_, phrase string) bool {
		capturedPhrase = phrase
		return false
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if capturedPhrase != "destroy perforce 123456789012" {
		t.Errorf("phrase = %q, want %q", capturedPhrase, "destroy perforce 123456789012")
	}
	assertContains(t, out.String(), "destroy perforce 123456789012")
}

// TestDestroyReadStateError verifies error is surfaced before any delete call.
func TestDestroyReadStateError(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, nil, nil)
	c.readState = func() (*fabricastate.State, error) {
		return nil, errors.New("disk read failure")
	}
	deleteCalled := false
	c.deleteResource = func(_ context.Context, _ *cloud.Resource) error {
		deleteCalled = true
		return nil
	}

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when readState fails")
	}
	assertContains(t, err.Error(), "reading state")
	if deleteCalled {
		t.Error("delete was called after readState failure")
	}
}

// TestDestroyNilProviderErrors verifies a nil deleteResource seam returns an error.
func TestDestroyNilProviderErrors(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.deleteResource = nil // simulate nil provider

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error with nil deleteResource")
	}
	assertContains(t, err.Error(), "no provider configured")
}

// TestDestroyWriteStateErrorSurfacedAsWarning verifies write errors don't abort the destroy.
func TestDestroyWriteStateErrorSurfacedAsWarning(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.writeState = func(_ *fabricastate.State) error {
		return errors.New("disk full")
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("writeState failure must not abort destroy: %v", err)
	}
	assertContains(t, out.String(), "Warning")
	assertContains(t, out.String(), "disk full")
}

// TestDestroyJSONNotProvisioned verifies JSON output when not provisioned.
func TestDestroyJSONNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, st, nil)
	c.jsonOut = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var result DestroyOutput
	if err := parseJSON(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if len(result.Destroyed) != 0 {
		t.Errorf("destroyed = %v, want empty", result.Destroyed)
	}
}

// TestDestroyJSONDryRun verifies JSON dry-run output contains resource IDs.
func TestDestroyJSONDryRun(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	c := newTestCommand(&out, st, nil)
	c.dryRun = true
	c.jsonOut = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var result DestroyOutput
	if err := parseJSON(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if !result.DryRun {
		t.Error("dryRun field must be true")
	}
	if len(result.Destroyed) != 2 {
		t.Errorf("expected 2 resources in dry-run output, got %d: %v", len(result.Destroyed), result.Destroyed)
	}
}

// TestDestroyJSONHappyPath verifies JSON output after successful destroy.
func TestDestroyJSONHappyPath(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
	c := newTestCommand(&out, st, nil)
	c.assumeYes = true
	c.jsonOut = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var result DestroyOutput
	if err := parseJSON(out.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, out.String())
	}
	if result.DryRun {
		t.Error("dryRun must be false on real destroy")
	}
	if len(result.Destroyed) != 2 {
		t.Errorf("expected 2 destroyed, got %d: %v", len(result.Destroyed), result.Destroyed)
	}
}

// TestDestroyAssumeYesSkipsPrompt verifies --yes proceeds without confirmation.
func TestDestroyAssumeYesSkipsPrompt(t *testing.T) {
	var out bytes.Buffer
	st := perforceState("provisioning", true)
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

// TestResourcesToDeleteOrder verifies Instance before SG regardless of storage order.
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

// ---- helpers ----

func getResource(m *fabricastate.ModuleState, typeName string) (fabricastate.ModuleResource, bool) {
	for _, r := range m.Resources {
		if r.TypeName == typeName {
			return r, true
		}
	}
	return fabricastate.ModuleResource{}, false
}

func parseJSON(data []byte, v any) error {
	return json.Unmarshal(data, v) //nolint:wrapcheck
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
	deleteErr map[string]error
	deleted   []string
}

func (f *fakeResourceClient) Delete(_ context.Context, r *cloud.Resource) error {
	if err, ok := f.deleteErr[r.TypeName]; ok {
		return err
	}
	f.deleted = append(f.deleted, r.Identifier)
	return nil
}

func (f *fakeResourceClient) Create(_ context.Context, _ *cloud.Resource) error { return nil }
func (f *fakeResourceClient) Get(_ context.Context, _ *cloud.Resource) error    { return nil }
func (f *fakeResourceClient) Update(_ context.Context, _ *cloud.Resource) error { return nil }
func (f *fakeResourceClient) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}
