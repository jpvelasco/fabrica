package rollback

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func stateWith(active, superseded string) *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	res := []fabricastate.ModuleResource{
		{TypeName: "AWS::GameLift::Alias", Identifier: "alias-1"},
		{TypeName: "AWS::GameLift::Fleet", Identifier: active, Properties: map[string]string{"role": "active", "buildVersion": "v2"}},
	}
	if superseded != "" {
		res = append(res, fabricastate.ModuleResource{
			TypeName: "AWS::GameLift::Fleet", Identifier: superseded,
			Properties: map[string]string{"role": "superseded", "buildVersion": "v1"},
		})
	}
	st.UpsertModule("deploy", "v2", "ready", res)
	return st
}

func newTestCmd(out *bytes.Buffer, st *fabricastate.State) *command {
	return &command{
		runtime:        globals.Runtime{Config: config.Defaults(), Provider: &testutil.GameLiftProvider{}},
		out:            out,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		writeState:     func(s *fabricastate.State) error { *st = *s; return nil },
		updateResource: func(context.Context, *cloud.Resource) error { return nil },
		fleetStatus: func(_ context.Context, id string) (cloud.FleetInfo, error) {
			return cloud.FleetInfo{FleetID: id, Status: "ACTIVE"}, nil
		},
		confirm: func(string) bool { return true },
	}
}

func TestRollbackHappyPath(t *testing.T) {
	var out bytes.Buffer
	st := stateWith("fleet-new", "fleet-old")
	c := newTestCmd(&out, st)
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "fleet-old") {
		t.Errorf("expected target fleet shown:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Next steps:") || !strings.Contains(out.String(), "fabrica deploy status") {
		t.Errorf("expected next-steps guidance:\n%s", out.String())
	}
	// Roles swapped: fleet-old now active, fleet-new superseded.
	m := st.GetModule("deploy")
	for _, r := range m.Resources {
		if r.Identifier == "fleet-old" && r.Properties["role"] != "active" {
			t.Errorf("fleet-old role = %q, want active", r.Properties["role"])
		}
		if r.Identifier == "fleet-new" && r.Properties["role"] != "superseded" {
			t.Errorf("fleet-new role = %q, want superseded", r.Properties["role"])
		}
	}
}

func TestRollbackNoCandidate(t *testing.T) {
	var out bytes.Buffer
	st := stateWith("fleet-new", "")
	c := newTestCmd(&out, st)
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error: nothing to roll back to")
	}
}

func TestRollbackTargetNotActive(t *testing.T) {
	var out bytes.Buffer
	st := stateWith("fleet-new", "fleet-old")
	c := newTestCmd(&out, st)
	c.fleetStatus = func(_ context.Context, id string) (cloud.FleetInfo, error) {
		return cloud.FleetInfo{FleetID: id, Status: "TERMINATED"}, nil
	}
	flipped := false
	c.updateResource = func(context.Context, *cloud.Resource) error { flipped = true; return nil }
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error: target fleet not ACTIVE")
	}
	if flipped {
		t.Error("must not flip when target is not ACTIVE")
	}
}

func TestRollbackConfirmRejected(t *testing.T) {
	var out bytes.Buffer
	st := stateWith("fleet-new", "fleet-old")
	c := newTestCmd(&out, st)
	c.confirm = func(string) bool { return false }
	flipped := false
	c.updateResource = func(context.Context, *cloud.Resource) error { flipped = true; return nil }
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if flipped {
		t.Error("rejected confirm should not flip")
	}
}

func TestRollbackReadStateError(t *testing.T) {
	var out bytes.Buffer
	st := stateWith("fleet-new", "fleet-old")
	c := newTestCmd(&out, st)
	c.readState = func() (*fabricastate.State, error) {
		return nil, fmt.Errorf("state read failed")
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "reading state") {
		t.Fatalf("expected error wrapping 'reading state', got: %v", err)
	}
}

func TestRollbackAliasNotFound(t *testing.T) {
	var out bytes.Buffer
	// State with deploy module that has only fleet resources, no alias.
	st := fabricastate.NewState("123456789012", "us-east-1")
	res := []fabricastate.ModuleResource{
		{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-new", Properties: map[string]string{"role": "active"}},
	}
	st.UpsertModule("deploy", "v2", "ready", res)
	c := newTestCmd(&out, st)
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "deploy alias not found") {
		t.Fatalf("expected 'deploy alias not found', got: %v", err)
	}
}

func TestRollbackFleetStatusNil(t *testing.T) {
	var out bytes.Buffer
	st := stateWith("fleet-new", "fleet-old")
	c := newTestCmd(&out, st)
	c.fleetStatus = nil
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "does not support GameLift") {
		t.Fatalf("expected 'does not support GameLift', got: %v", err)
	}
}

func TestRollbackFleetStatusError(t *testing.T) {
	var out bytes.Buffer
	st := stateWith("fleet-new", "fleet-old")
	c := newTestCmd(&out, st)
	c.fleetStatus = func(context.Context, string) (cloud.FleetInfo, error) {
		return cloud.FleetInfo{}, fmt.Errorf("fleet status error")
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "checking rollback target fleet") {
		t.Fatalf("expected 'checking rollback target fleet', got: %v", err)
	}
}

func TestRollbackUpdateResourceError(t *testing.T) {
	var out bytes.Buffer
	st := stateWith("fleet-new", "fleet-old")
	c := newTestCmd(&out, st)
	c.updateResource = func(context.Context, *cloud.Resource) error {
		return fmt.Errorf("alias update failed")
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "flipping alias") {
		t.Fatalf("expected 'flipping alias', got: %v", err)
	}
}

func TestRollbackWriteStateError(t *testing.T) {
	var out bytes.Buffer
	st := stateWith("fleet-new", "fleet-old")
	c := newTestCmd(&out, st)
	c.writeState = func(*fabricastate.State) error {
		return fmt.Errorf("state write failed")
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "writing state") {
		t.Fatalf("expected 'writing state', got: %v", err)
	}
}

func TestSwapRolesNilProperties(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	res := []fabricastate.ModuleResource{
		{TypeName: "AWS::GameLift::Alias", Identifier: "alias-1"},
		{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-a"},
		{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-b", Properties: nil},
	}
	st.UpsertModule("deploy", "v2", "ready", res)
	m := st.GetModule("deploy")

	// fleet-a has no Properties at all; fleet-b has nil Properties.
	// swapRoles should initialize both and set roles.
	swapRoles(m, "fleet-b", "fleet-a")

	for _, r := range m.Resources {
		if r.TypeName != "AWS::GameLift::Fleet" {
			continue
		}
		if r.Properties == nil {
			t.Fatalf("fleet %s: Properties is nil after swapRoles", r.Identifier)
		}
		switch r.Identifier {
		case "fleet-b":
			if r.Properties["role"] != "active" {
				t.Errorf("fleet-b role = %q, want active", r.Properties["role"])
			}
		case "fleet-a":
			if r.Properties["role"] != "superseded" {
				t.Errorf("fleet-a role = %q, want superseded", r.Properties["role"])
			}
		}
	}
}

// TestRollbackLockHeldAborts verifies a held state lock aborts rollback.
func TestRollbackLockHeldAborts(t *testing.T) {
	var out bytes.Buffer
	st := stateWith("fleet-new", "fleet-old")
	c := newTestCmd(&out, st)
	c.runtime.Provider = &testutil.LockingProvider{TestProvider: &testutil.TestProvider{}, Held: true}

	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "another fabrica run holds the state lock") {
		t.Fatalf("err = %v, want held-lock abort", err)
	}
}

// TestRollbackDryRunNoWrites verifies dry run renders the preview and skips
// the alias flip and the state write.
func TestRollbackDryRunNoWrites(t *testing.T) {
	var out bytes.Buffer
	st := stateWith("fleet-new", "fleet-old")
	c := newTestCmd(&out, st)
	c.dryRun = true
	updates, writes := 0, 0
	c.updateResource = func(context.Context, *cloud.Resource) error { updates++; return nil }
	c.writeState = func(*fabricastate.State) error { writes++; return nil }

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if updates != 0 || writes != 0 {
		t.Errorf("dry run must not mutate: updates = %d, writes = %d", updates, writes)
	}
	if !strings.Contains(out.String(), "fleet-old") {
		t.Errorf("expected preview naming the target fleet:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "dry run") {
		t.Errorf("expected dry-run preview header:\n%s", out.String())
	}
}

// TestRollbackUsesRecordedTargetOverCreationOrder is the #521 regression:
// promote A/B/C, roll back to B, promote D. Creation order is a, b, c, d with
// a/b/c all superseded; the legacy "last superseded" heuristic would pick c,
// but the fleet that actually served before D is B.
func TestRollbackUsesRecordedTargetOverCreationOrder(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	res := []fabricastate.ModuleResource{
		{TypeName: "AWS::GameLift::Alias", Identifier: "alias-1"},
		{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-a", Properties: map[string]string{"role": "superseded", "buildVersion": "v1"}},
		{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-b", Properties: map[string]string{"role": "superseded", "buildVersion": "v2", "rollbackTarget": "fleet-a"}},
		{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-c", Properties: map[string]string{"role": "superseded", "buildVersion": "v3", "rollbackTarget": "fleet-b"}},
		{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-d", Properties: map[string]string{"role": "active", "buildVersion": "v4", "rollbackTarget": "fleet-b"}},
	}
	st.UpsertModule("deploy", "v4", "ready", res)
	c := newTestCmd(&out, st)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(out.String(), "fleet-c") {
		t.Errorf("fleet-c (last superseded in creation order) must not be selected:\n%s", out.String())
	}
	m := st.GetModule("deploy")
	for _, r := range m.Resources {
		if r.Identifier == "fleet-b" && r.Properties["role"] != "active" {
			t.Errorf("fleet-b role = %q, want active", r.Properties["role"])
		}
		if r.Identifier == "fleet-d" && r.Properties["role"] != "superseded" {
			t.Errorf("fleet-d role = %q, want superseded", r.Properties["role"])
		}
	}
	if m.Version != "v2" {
		t.Errorf("module version = %q, want v2 (the build that now serves)", m.Version)
	}
}

// TestRollbackLegacyFallbackWarns verifies that state written by older fabrica
// versions (no recorded rollbackTarget) still rolls back to the most-recent
// superseded fleet, with an operator-facing note.
func TestRollbackLegacyFallbackWarns(t *testing.T) {
	var out bytes.Buffer
	st := stateWith("fleet-new", "fleet-old") // no rollbackTarget recorded
	c := newTestCmd(&out, st)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "no recorded rollback target") {
		t.Errorf("expected legacy-fallback note:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "fleet-old") {
		t.Errorf("expected the most-recent superseded fleet as target:\n%s", out.String())
	}
}

// TestRollbackUpdatesModuleVersion is the #522 regression: after the alias
// flip the module version and state timestamp must reflect the build that
// now serves.
func TestRollbackUpdatesModuleVersion(t *testing.T) {
	var out bytes.Buffer
	st := stateWith("fleet-new", "fleet-old") // active build v2, superseded build v1
	updatedBefore := st.Updated
	c := newTestCmd(&out, st)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	m := st.GetModule("deploy")
	if m.Version != "v1" {
		t.Errorf("module version = %q, want v1 (rolled-back build)", m.Version)
	}
	if st.Updated.Before(updatedBefore) {
		t.Errorf("state Updated = %s, want not before %s", st.Updated, updatedBefore)
	}
}

// TestRollbackKeepsVersionWithoutBuildMetadata verifies older records whose
// fleet carries no buildVersion keep the current module version.
func TestRollbackKeepsVersionWithoutBuildMetadata(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	res := []fabricastate.ModuleResource{
		{TypeName: "AWS::GameLift::Alias", Identifier: "alias-1"},
		{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-new", Properties: map[string]string{"role": "active", "rollbackTarget": "fleet-old"}},
		{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-old", Properties: map[string]string{"role": "superseded"}},
	}
	st.UpsertModule("deploy", "v2", "ready", res)
	c := newTestCmd(&out, st)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := st.GetModule("deploy").Version; got != "v2" {
		t.Errorf("module version = %q, want v2 (kept when the target has no buildVersion)", got)
	}
}

// TestRollbackDryRunDoesNotAcquireLock verifies dry run succeeds without
// touching the state lock, even when another run holds it.
func TestRollbackDryRunDoesNotAcquireLock(t *testing.T) {
	var out bytes.Buffer
	st := stateWith("fleet-new", "fleet-old")
	c := newTestCmd(&out, st)
	c.dryRun = true
	locker := &testutil.LockingProvider{TestProvider: &testutil.TestProvider{}, Held: true}
	c.runtime.Provider = locker

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("dry run must succeed without a lock (even when held): %v", err)
	}
	if locker.Acquires != 0 {
		t.Errorf("expected 0 lock acquires in dry run, got %d", locker.Acquires)
	}
}
