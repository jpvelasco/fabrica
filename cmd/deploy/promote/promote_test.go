package promote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func seededState() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("deploy", "fabrica-deploy", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-deploy-gamelift"},
		{TypeName: "AWS::GameLift::Alias", Identifier: "alias-1"},
	})
	return st
}

func baseRuntime() globals.Runtime {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.Deploy.BuildBucket = "bkt"
	return globals.Runtime{Config: cfg, Provider: &testutil.GameLiftProvider{}}
}

func newTestCmd(out *bytes.Buffer, st *fabricastate.State) *command {
	statuses := []string{"BUILDING", "ACTIVATING", "ACTIVE"}
	i := 0
	return &command{
		runtime:      baseRuntime(),
		buildVersion: "v1.2.3",
		wait:         true,
		out:          out,
		costs:        fabricacost.Global,
		readState:    func() (*fabricastate.State, error) { return st, nil },
		writeState:   func(s *fabricastate.State) error { *st = *s; return nil },
		createResource: func(_ context.Context, r *cloud.Resource) error {
			r.Identifier = "build-123"
			return nil
		},
		createFleetAsync: func(_ context.Context, r *cloud.Resource) error {
			r.Identifier = "fleet-new"
			return nil
		},
		updateResource: func(_ context.Context, _ *cloud.Resource) error { return nil },
		getResource:    func(_ context.Context, _ *cloud.Resource) error { return nil },
		fleetStatus: func(_ context.Context, id string) (cloud.FleetInfo, error) {
			s := statuses[i]
			if i < len(statuses)-1 {
				i++
			}
			return cloud.FleetInfo{FleetID: id, Status: s}, nil
		},
		fleetEvents: func(context.Context, string) ([]cloud.FleetEvent, error) { return nil, nil },
		confirm:     func(string) bool { return true },
		waitCtx:     func(context.Context, time.Duration) error { return nil },
		now:         time.Now,
	}
}

func TestPromoteHappyPath(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "ACTIVE") || !strings.Contains(s, "alias") {
		t.Errorf("expected activation + alias flip:\n%s", s)
	}
	// New fleet recorded as active.
	m := st.GetModule("deploy")
	var found bool
	for _, r := range m.Resources {
		if r.TypeName == "AWS::GameLift::Fleet" && r.Identifier == "fleet-new" {
			found = true
			if r.Properties["role"] != "active" {
				t.Errorf("new fleet role = %q", r.Properties["role"])
			}
			if r.Properties["buildVersion"] != "v1.2.3" {
				t.Errorf("buildVersion = %q", r.Properties["buildVersion"])
			}
		}
	}
	if !found {
		t.Error("new fleet not recorded in state")
	}
}

func TestPromoteRequiresSetup(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1") // no deploy module
	c := newTestCmd(&out, st)
	c.assumeYes = true
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error: deploy not set up")
	}
}

func TestPromoteFleetErrorNoFlip(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.fleetStatus = func(_ context.Context, id string) (cloud.FleetInfo, error) {
		return cloud.FleetInfo{FleetID: id, Status: "ERROR"}, nil
	}
	flipped := false
	c.updateResource = func(context.Context, *cloud.Resource) error { flipped = true; return nil }
	c.fleetEvents = func(context.Context, string) ([]cloud.FleetEvent, error) {
		return []cloud.FleetEvent{{Code: "FLEET_STATE_ERROR", Message: "bad launch path"}}, nil
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error on fleet ERROR")
	}
	if flipped {
		t.Error("alias must NOT flip when fleet errors")
	}
	if !strings.Contains(out.String(), "bad launch path") {
		t.Errorf("expected fleet events surfaced:\n%s", out.String())
	}
}

func TestPromoteBuildFailsRecoverable(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.createResource = func(context.Context, *cloud.Resource) error { return errors.New("s3 access denied") }
	fleetCreated := false
	c.createFleetAsync = func(context.Context, *cloud.Resource) error { fleetCreated = true; return nil }
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected build registration error")
	}
	if fleetCreated {
		t.Error("fleet must not be created if build registration fails")
	}
}

func TestPromoteDryRunOpenCIDRWarning(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.dryRun = true
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	// Default AllowedCIDR is 0.0.0.0/0 — the warning must appear in the plan.
	if !strings.Contains(out.String(), "WARNING: deploy.allowedCidr is 0.0.0.0/0") {
		t.Errorf("expected open-CIDR warning in dry-run output:\n%s", out.String())
	}
}

func TestPromoteDryRunNoOpenCIDRWarning(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.dryRun = true
	c.runtime.Config.Deploy.AllowedCIDR = "10.0.0.0/8"
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "WARNING: deploy.allowedCidr is 0.0.0.0/0") {
		t.Errorf("open-CIDR warning must not appear with a private CIDR:\n%s", got)
	}
	if !strings.Contains(got, "UDP inbound:   10.0.0.0/8") {
		t.Errorf("expected plan to show the configured CIDR:\n%s", got)
	}
}

func TestPromoteDryRunNoWrites(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.dryRun = true
	builds := 0
	c.createResource = func(context.Context, *cloud.Resource) error { builds++; return nil }
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if builds != 0 {
		t.Errorf("dry-run registered %d builds", builds)
	}
	if !strings.Contains(out.String(), "Cost estimate") {
		t.Errorf("dry-run should show cost:\n%s", out.String())
	}
}

func TestPromoteAliasFlipFailsAfterActive(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.updateResource = func(context.Context, *cloud.Resource) error { return errors.New("throttled") }
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected alias-flip error")
	}
	if !strings.Contains(err.Error(), "ACTIVE") {
		t.Errorf("error should explain fleet is ACTIVE but alias not flipped: %v", err)
	}
}

// ---- Uncovered paths: run ----

func TestPromoteNoProvider(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.runtime.Provider = nil
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when provider is nil")
	}
}

func TestPromoteMissingRoleInState(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("deploy", "fabrica-deploy", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::GameLift::Alias", Identifier: "alias-1"},
	})
	c := newTestCmd(&out, st)
	c.assumeYes = true
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when IAM role is missing")
	}
}

func TestPromoteMissingAliasInState(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("deploy", "fabrica-deploy", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-deploy-gamelift"},
	})
	c := newTestCmd(&out, st)
	c.assumeYes = true
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when alias is missing")
	}
}

func TestPromoteConfirmRejected(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.confirm = func(string) bool { return false }
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("confirm rejection should return nil: %v", err)
	}
	if !strings.Contains(out.String(), "cancelled") {
		t.Errorf("expected cancellation message:\n%s", out.String())
	}
}

func TestPromoteReadStateError(t *testing.T) {
	var out bytes.Buffer
	c := newTestCmd(&out, seededState())
	c.assumeYes = true
	c.readState = func() (*fabricastate.State, error) { return nil, errors.New("state file corrupted") }
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when readState fails")
	}
}

// ---- Uncovered paths: apply ----

func TestPromoteApplyNoCreateResource(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.createResource = nil
	c.createFleetAsync = nil
	m := st.GetModule("deploy")
	plan := c.runtime.Config.Deploy
	_ = plan
	err := c.apply(context.Background(), st, m, nil)
	if err == nil {
		t.Fatal("expected error when createResource is nil")
	}
}

// ---- Uncovered paths: pollUntilActive ----

func TestPromotePollFleetStatusError(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.fleetStatus = func(context.Context, string) (cloud.FleetInfo, error) {
		return cloud.FleetInfo{}, errors.New("access denied")
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when fleetStatus fails")
	}
}

func TestPromotePollFleetDeletting(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.fleetStatus = func(_ context.Context, id string) (cloud.FleetInfo, error) {
		return cloud.FleetInfo{FleetID: id, Status: "DELETING"}, nil
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when fleet is DELETING")
	}
}

func TestPromotePollFleetTerminated(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.fleetStatus = func(_ context.Context, id string) (cloud.FleetInfo, error) {
		return cloud.FleetInfo{FleetID: id, Status: "TERMINATED"}, nil
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when fleet is TERMINATED")
	}
}

func TestPromotePollTimeout(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	called := 0
	c.fleetStatus = func(_ context.Context, id string) (cloud.FleetInfo, error) {
		called++
		return cloud.FleetInfo{FleetID: id, Status: "ACTIVATING"}, nil
	}
	baseTime := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	nowCalls := 0
	c.now = func() time.Time {
		nowCalls++
		// First call sets the deadline (baseTime + 45min). After that, return a time past it.
		if nowCalls == 1 {
			return baseTime
		}
		return baseTime.Add(46 * time.Minute)
	}
	c.waitCtx = func(context.Context, time.Duration) error { return nil }
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout message: %v", err)
	}
}

// TestPromoteActiveWaitsForCapacity verifies an ACTIVE fleet that has not yet
// filled the requested instance count is re-polled (alias not flipped) until
// the active count reaches the plan's desired count.
func TestPromoteActiveWaitsForCapacity(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.runtime.Config.Deploy.DesiredInstances = 3
	polls := 0
	c.fleetStatus = func(_ context.Context, id string) (cloud.FleetInfo, error) {
		polls++
		info := cloud.FleetInfo{FleetID: id, Status: "ACTIVE", CapacityDesired: 3}
		// First two polls the fleet is ACTIVE but short of capacity.
		if polls <= 2 {
			info.CapacityCurrent = 1
		} else {
			info.CapacityCurrent = 3
		}
		return info, nil
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if polls != 3 {
		t.Errorf("polls = %d, want 3 (ACTIVE under capacity must be re-polled)", polls)
	}
	s := out.String()
	if !strings.Contains(s, "waiting for the requested capacity") {
		t.Errorf("expected capacity-wait line:\n%s", s)
	}
	if !strings.Contains(s, "live on fleet") {
		t.Errorf("expected promote completion after capacity filled:\n%s", s)
	}
	m := st.GetModule("deploy")
	var active bool
	for _, r := range m.Resources {
		if r.TypeName == "AWS::GameLift::Fleet" && r.Identifier == "fleet-new" && r.Properties["role"] == "active" {
			active = true
		}
	}
	if !active {
		t.Error("new fleet must be recorded active after capacity is met")
	}
}

// TestPromoteActiveUnderCapacityTimesOut verifies a fleet that is ACTIVE but
// never fills the requested capacity fails closed at the deadline instead of
// flipping the alias to an under-provisioned fleet.
func TestPromoteActiveUnderCapacityTimesOut(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.fleetStatus = func(_ context.Context, id string) (cloud.FleetInfo, error) {
		return cloud.FleetInfo{FleetID: id, Status: "ACTIVE", CapacityDesired: 1, CapacityCurrent: 0}, nil
	}
	baseTime := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	nowCalls := 0
	c.now = func() time.Time {
		nowCalls++
		if nowCalls == 1 {
			return baseTime
		}
		return baseTime.Add(46 * time.Minute)
	}
	c.waitCtx = func(context.Context, time.Duration) error { return nil }
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected timeout when fleet is ACTIVE but never reaches capacity")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout message: %v", err)
	}
	if !strings.Contains(err.Error(), "0 active of 1 desired") {
		t.Errorf("error should include the capacity detail: %v", err)
	}
}

// ---- Uncovered paths: printFleetEvents ----

func TestPromotePrintFleetEventsNil(t *testing.T) {
	var out bytes.Buffer
	c := newTestCmd(&out, seededState())
	c.fleetEvents = nil
	// Should not panic when fleetEvents is nil.
	c.printFleetEvents(context.Background(), "fleet-123")
}

func TestPromotePrintFleetEventsError(t *testing.T) {
	var out bytes.Buffer
	c := newTestCmd(&out, seededState())
	c.fleetEvents = func(context.Context, string) ([]cloud.FleetEvent, error) {
		return nil, errors.New("events unavailable")
	}
	// Should not panic; silently returns on error.
	c.printFleetEvents(context.Background(), "fleet-123")
}

func TestPromotePrintFleetEventsEmpty(t *testing.T) {
	var out bytes.Buffer
	c := newTestCmd(&out, seededState())
	c.fleetEvents = func(context.Context, string) ([]cloud.FleetEvent, error) {
		return []cloud.FleetEvent{}, nil
	}
	// Should not panic; silently returns on empty.
	c.printFleetEvents(context.Background(), "fleet-123")
}

// ---- Uncovered paths: recordResource (update-existing) ----

func TestPromoteRecordResourceUpdatesExisting(t *testing.T) {
	st := seededState()
	m := st.GetModule("deploy")
	c := &command{out: io.Discard}

	// Pre-populate with an existing fleet resource.
	m.Resources = append(m.Resources, fabricastate.ModuleResource{
		TypeName:   "AWS::GameLift::Fleet",
		Identifier: "fleet-old",
		Properties: map[string]string{"buildVersion": "v1.0.0"},
	})

	// Record the same resource again — should update in place.
	c.recordResource(m, fabricastate.ModuleResource{
		TypeName:   "AWS::GameLift::Fleet",
		Identifier: "fleet-old",
		Properties: map[string]string{"buildVersion": "v1.2.3"},
	})

	// Should still have exactly 3 resources (role, alias, fleet) — no duplicate.
	if len(m.Resources) != 3 {
		t.Errorf("expected 3 resources after update, got %d", len(m.Resources))
	}
	// Verify the property was updated.
	for _, r := range m.Resources {
		if r.TypeName == "AWS::GameLift::Fleet" && r.Identifier == "fleet-old" {
			if r.Properties["buildVersion"] != "v1.2.3" {
				t.Errorf("buildVersion = %q, want v1.2.3", r.Properties["buildVersion"])
			}
		}
	}
}

// ---- Uncovered paths: swapActiveFleet ----

func TestPromoteSwapActiveFleetSupersedesOld(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::IAM::Role", Identifier: "fabrica-deploy-gamelift"},
			{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-old", Properties: map[string]string{"role": "active"}},
		},
	}
	c := &command{out: io.Discard}
	c.swapActiveFleet(m, "fleet-new")

	// Old fleet should be superseded.
	for _, r := range m.Resources {
		if r.Identifier == "fleet-old" && r.TypeName == "AWS::GameLift::Fleet" {
			if r.Properties["role"] != "superseded" {
				t.Errorf("old fleet role = %q, want superseded", r.Properties["role"])
			}
		}
	}
}

// TestPromoteSwapActiveFleetRecordsRollbackTarget verifies the new active
// fleet records the demoted fleet as its explicit rollback target.
func TestPromoteSwapActiveFleetRecordsRollbackTarget(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-old", Properties: map[string]string{"role": "active", "buildVersion": "v1"}},
			{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-new", Properties: map[string]string{"role": "provisioning", "buildVersion": "v2"}},
		},
	}
	c := &command{out: io.Discard}
	c.swapActiveFleet(m, "fleet-new")

	for _, r := range m.Resources {
		if r.Identifier == "fleet-new" && r.TypeName == "AWS::GameLift::Fleet" {
			if r.Properties["role"] != "active" {
				t.Errorf("new fleet role = %q, want active", r.Properties["role"])
			}
			if r.Properties["rollbackTarget"] != "fleet-old" {
				t.Errorf("rollbackTarget = %q, want fleet-old", r.Properties["rollbackTarget"])
			}
		}
	}
}

// TestPromoteSwapActiveFleetFirstPromoteHasNoTarget verifies a first
// promotion records no rollback target (there is no predecessor).
func TestPromoteSwapActiveFleetFirstPromoteHasNoTarget(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-new", Properties: map[string]string{"role": "provisioning"}},
		},
	}
	c := &command{out: io.Discard}
	c.swapActiveFleet(m, "fleet-new")

	for _, r := range m.Resources {
		if r.Identifier == "fleet-new" {
			if r.Properties["role"] != "active" {
				t.Errorf("role = %q, want active", r.Properties["role"])
			}
			if _, ok := r.Properties["rollbackTarget"]; ok {
				t.Errorf("first promotion must not record a rollback target, got %q", r.Properties["rollbackTarget"])
			}
		}
	}
}

func TestPromoteSwapActiveFleetNilProperties(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-1", Properties: nil},
		},
	}
	c := &command{out: io.Discard}
	// Should not panic on nil Properties.
	c.swapActiveFleet(m, "fleet-1")

	for _, r := range m.Resources {
		if r.Identifier == "fleet-1" {
			if r.Properties == nil {
				t.Error("Properties should be initialized even if nil initially")
			}
			if r.Properties["role"] != "active" {
				t.Errorf("role = %q, want active", r.Properties["role"])
			}
		}
	}
}

// ---- No-wait path ----

func TestPromoteNoWaitSkipsPoll(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.wait = false
	polled := false
	c.fleetStatus = func(context.Context, string) (cloud.FleetInfo, error) {
		polled = true
		return cloud.FleetInfo{}, nil
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("no-wait run: %v", err)
	}
	if polled {
		t.Error("fleet should not be polled when --no-wait")
	}
	if !strings.Contains(out.String(), "Fleet creation started") {
		t.Errorf("expected no-wait message:\n%s", out.String())
	}
}

// ---- Incremental state-write failures ----

// TestPromoteBuildStateWriteErrorStopsBeforeFleet verifies a failed state write
// after build registration aborts promote BEFORE creating the fleet, so the
// build never exists untracked alongside a half-created fleet.
func TestPromoteBuildStateWriteErrorStopsBeforeFleet(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	fleets := 0
	c.createFleetAsync = func(_ context.Context, r *cloud.Resource) error {
		fleets++
		r.Identifier = "fleet-new"
		return nil
	}
	c.writeState = func(*fabricastate.State) error { return errors.New("disk full") }
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when state write fails after build registration")
	}
	if !strings.Contains(err.Error(), "recording GameLift build") {
		t.Errorf("error = %q, want build state-write context", err)
	}
	if fleets != 0 {
		t.Errorf("fleet created %d times despite failed build state write; want 0", fleets)
	}
}

// TestPromoteNoWaitFleetStateWriteErrorFails verifies that under --no-wait a
// failed post-fleet state write surfaces as an error instead of a false success.
func TestPromoteNoWaitFleetStateWriteErrorFails(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.wait = false
	writes := 0
	c.writeState = func(s *fabricastate.State) error {
		writes++
		if writes >= 2 {
			return errors.New("disk full")
		}
		*st = *s
		return nil
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when post-fleet state write fails under --no-wait")
	}
	if !strings.Contains(err.Error(), "recording fleet") {
		t.Errorf("error = %q, want fleet state-write context", err)
	}
}

// TestPromoteWaitCancelled verifies cancellation during fleet polling returns
// an explicit error stating the alias was not changed.
func TestPromoteWaitCancelled(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.fleetStatus = func(_ context.Context, id string) (cloud.FleetInfo, error) {
		return cloud.FleetInfo{FleetID: id, Status: "CREATED"}, nil
	}
	c.waitCtx = func(context.Context, time.Duration) error { return context.Canceled }

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected cancellation error during fleet wait")
	}
	if !strings.Contains(err.Error(), "cancelled") || !strings.Contains(err.Error(), "alias was not changed") {
		t.Errorf("error = %v, want cancelled + alias-not-changed guidance", err)
	}
}

// TestPromoteLockHeldAborts verifies a held state lock aborts promote before
// any state read or AWS call.
func TestPromoteDryRunDoesNotAcquireLock(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.dryRun = true
	locker := &testutil.LockingProvider{TestProvider: &testutil.TestProvider{}, Held: true}
	c.runtime.Provider = locker
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("dry-run must skip lock: %v", err)
	}
	if locker.Acquires != 0 {
		t.Errorf("dry-run acquires = %d, want 0", locker.Acquires)
	}
}

func TestPromoteLockHeldAborts(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.runtime.Provider = &testutil.LockingProvider{TestProvider: &testutil.TestProvider{}, Held: true}

	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "another fabrica run holds the state lock") {
		t.Fatalf("err = %v, want held-lock abort", err)
	}
}

// ---- Pending promote resume ----

// pendingState seeds a recorded promote for the default version (v1.2.3): a
// registered build plus a fleet still in role "provisioning".
func pendingState() *fabricastate.State {
	st := seededState()
	m := st.GetModule("deploy")
	m.Resources = append(m.Resources,
		fabricastate.ModuleResource{
			TypeName:   "AWS::GameLift::Build",
			Identifier: "build-123",
			Properties: map[string]string{"buildVersion": "v1.2.3"},
		},
		fabricastate.ModuleResource{
			TypeName:   "AWS::GameLift::Fleet",
			Identifier: "fleet-pending",
			Properties: map[string]string{"buildVersion": "v1.2.3", "role": "provisioning"},
		},
	)
	return st
}

// TestPromoteNoWaitThenResumeNoDuplicate verifies the documented recovery
// path: a --no-wait promote followed by a re-run without --no-wait resumes
// the recorded pending deployment (one build, one fleet) instead of
// registering a second build and creating a second fleet.
func TestPromoteNoWaitThenResumeNoDuplicate(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	buildCreates, fleetCreates, flips := 0, 0, 0
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.createResource = func(_ context.Context, r *cloud.Resource) error {
		buildCreates++
		r.Identifier = "build-1"
		return nil
	}
	c.createFleetAsync = func(_ context.Context, r *cloud.Resource) error {
		fleetCreates++
		r.Identifier = "fleet-1"
		return nil
	}
	c.updateResource = func(context.Context, *cloud.Resource) error { flips++; return nil }

	// First run: --no-wait starts the fleet and returns.
	c.wait = false
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("no-wait run: %v", err)
	}

	// Second run: re-run without --no-wait resumes the pending deployment.
	c.wait = true
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("resume run: %v", err)
	}

	if buildCreates != 1 || fleetCreates != 1 {
		t.Errorf("buildCreates = %d, fleetCreates = %d, want 1 and 1", buildCreates, fleetCreates)
	}
	if flips != 1 {
		t.Errorf("alias flips = %d, want 1", flips)
	}
	m := st.GetModule("deploy")
	fleets, builds := 0, 0
	for _, r := range m.Resources {
		switch r.TypeName {
		case "AWS::GameLift::Fleet":
			fleets++
			if r.Properties["role"] != "active" {
				t.Errorf("fleet %s role = %q, want active", r.Identifier, r.Properties["role"])
			}
		case "AWS::GameLift::Build":
			builds++
		}
	}
	if fleets != 1 || builds != 1 {
		t.Errorf("recorded fleets = %d, builds = %d, want 1 and 1", fleets, builds)
	}
	s := out.String()
	if !strings.Contains(s, "Resuming pending promote") {
		t.Errorf("expected resume message:\n%s", s)
	}
	if !strings.Contains(s, "live on fleet fleet-1") {
		t.Errorf("expected completion on the original fleet:\n%s", s)
	}
}

// TestPromoteResumeAfterAliasFlipFailure verifies a re-run after a failed
// alias flip makes no new create calls: it polls the recorded fleet, flips
// the alias, and completes.
func TestPromoteResumeAfterAliasFlipFailure(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	buildCreates, fleetCreates, flips := 0, 0, 0
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.createResource = func(_ context.Context, r *cloud.Resource) error {
		buildCreates++
		r.Identifier = "build-1"
		return nil
	}
	c.createFleetAsync = func(_ context.Context, r *cloud.Resource) error {
		fleetCreates++
		r.Identifier = "fleet-1"
		return nil
	}
	c.fleetStatus = func(_ context.Context, id string) (cloud.FleetInfo, error) {
		return cloud.FleetInfo{FleetID: id, Status: "ACTIVE"}, nil
	}
	c.updateResource = func(context.Context, *cloud.Resource) error { return errors.New("throttled") }

	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "flipping the alias failed") {
		t.Fatalf("expected alias-flip error on first run, got %v", err)
	}

	// Re-run on the same state: the recorded pending deployment is resumed.
	var out2 bytes.Buffer
	c2 := newTestCmd(&out2, st)
	c2.assumeYes = true
	c2.createResource = c.createResource
	c2.createFleetAsync = c.createFleetAsync
	c2.fleetStatus = c.fleetStatus
	c2.updateResource = func(context.Context, *cloud.Resource) error { flips++; return nil }
	if err := c2.run(context.Background()); err != nil {
		t.Fatalf("resume run: %v", err)
	}
	if buildCreates != 1 || fleetCreates != 1 {
		t.Errorf("buildCreates = %d, fleetCreates = %d, want 1 and 1 (resume must not re-create)", buildCreates, fleetCreates)
	}
	if flips != 1 {
		t.Errorf("alias flips = %d, want 1", flips)
	}
	if !strings.Contains(out2.String(), "Resuming pending promote") {
		t.Errorf("expected resume message:\n%s", out2.String())
	}
}

// TestPromoteResumeAfterPollFailure verifies a re-run after a fleet ERROR
// during polling resumes the recorded deployment with no new create calls.
func TestPromoteResumeAfterPollFailure(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	buildCreates, fleetCreates := 0, 0
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.createResource = func(_ context.Context, r *cloud.Resource) error {
		buildCreates++
		r.Identifier = "build-1"
		return nil
	}
	c.createFleetAsync = func(_ context.Context, r *cloud.Resource) error {
		fleetCreates++
		r.Identifier = "fleet-1"
		return nil
	}
	c.fleetStatus = func(_ context.Context, id string) (cloud.FleetInfo, error) {
		return cloud.FleetInfo{FleetID: id, Status: "ERROR"}, nil
	}
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "entered status ERROR") {
		t.Fatalf("expected fleet ERROR on first run, got %v", err)
	}

	// Re-run: the default status sequence reaches ACTIVE for the resume poll.
	var out2 bytes.Buffer
	c2 := newTestCmd(&out2, st)
	c2.assumeYes = true
	c2.createResource = func(_ context.Context, r *cloud.Resource) error {
		buildCreates++
		r.Identifier = "build-1"
		return nil
	}
	c2.createFleetAsync = func(_ context.Context, r *cloud.Resource) error {
		fleetCreates++
		r.Identifier = "fleet-1"
		return nil
	}
	if err := c2.run(context.Background()); err != nil {
		t.Fatalf("resume run: %v", err)
	}
	if buildCreates != 1 || fleetCreates != 1 {
		t.Errorf("buildCreates = %d, fleetCreates = %d, want 1 and 1", buildCreates, fleetCreates)
	}
	if !strings.Contains(out2.String(), "live on fleet fleet-1") {
		t.Errorf("expected completion on the original fleet:\n%s", out2.String())
	}
}

// TestPromoteAmbiguousPendingMultipleFleets verifies two recorded pending
// fleets fail with a loud error naming both, and no resources are created.
func TestPromoteAmbiguousPendingMultipleFleets(t *testing.T) {
	var out bytes.Buffer
	st := pendingState()
	m := st.GetModule("deploy")
	m.Resources = append(m.Resources, fabricastate.ModuleResource{
		TypeName:   "AWS::GameLift::Fleet",
		Identifier: "fleet-other",
		Properties: map[string]string{"buildVersion": "v1.2.3", "role": "provisioning"},
	})
	c := newTestCmd(&out, st)
	c.assumeYes = true
	builds := 0
	c.createResource = func(context.Context, *cloud.Resource) error { builds++; return nil }
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "multiple pending deploys") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
	if !strings.Contains(err.Error(), "fleet-pending, fleet-other") {
		t.Errorf("error should name both fleets: %v", err)
	}
	if builds != 0 {
		t.Errorf("no build may be registered on ambiguity, got %d", builds)
	}
}

// TestPromotePendingFleetDifferentVersion verifies a pending fleet recorded
// for another build version is not adopted by a promote of a new version.
func TestPromotePendingFleetDifferentVersion(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	m := st.GetModule("deploy")
	m.Resources = append(m.Resources,
		fabricastate.ModuleResource{
			TypeName:   "AWS::GameLift::Build",
			Identifier: "build-old",
			Properties: map[string]string{"buildVersion": "v1.0.0"},
		},
		fabricastate.ModuleResource{
			TypeName:   "AWS::GameLift::Fleet",
			Identifier: "fleet-old",
			Properties: map[string]string{"buildVersion": "v1.0.0", "role": "provisioning"},
		},
	)
	c := newTestCmd(&out, st)
	c.assumeYes = true
	builds := 0
	c.createResource = func(context.Context, *cloud.Resource) error { builds++; return nil }
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "v1.0.0") || !strings.Contains(err.Error(), "v1.2.3") {
		t.Fatalf("expected version-mismatch error, got %v", err)
	}
	if builds != 0 {
		t.Errorf("no build may be registered while another version is pending, got %d", builds)
	}
}

// TestPromotePendingFleetNoBuildRecord verifies a pending fleet with no
// matching build record fails with a loud error.
func TestPromotePendingFleetNoBuildRecord(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	m := st.GetModule("deploy")
	m.Resources = append(m.Resources, fabricastate.ModuleResource{
		TypeName:   "AWS::GameLift::Fleet",
		Identifier: "fleet-pending",
		Properties: map[string]string{"buildVersion": "v1.2.3", "role": "provisioning"},
	})
	c := newTestCmd(&out, st)
	c.assumeYes = true
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no recorded build for v1.2.3") {
		t.Fatalf("expected missing-build error, got %v", err)
	}
}

// TestPromotePendingFleetMultipleBuildRecords verifies a pending fleet with
// several matching build records fails as ambiguous.
func TestPromotePendingFleetMultipleBuildRecords(t *testing.T) {
	var out bytes.Buffer
	st := pendingState()
	m := st.GetModule("deploy")
	m.Resources = append(m.Resources, fabricastate.ModuleResource{
		TypeName:   "AWS::GameLift::Build",
		Identifier: "build-again",
		Properties: map[string]string{"buildVersion": "v1.2.3"},
	})
	c := newTestCmd(&out, st)
	c.assumeYes = true
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "ambiguous pending deploy") || !strings.Contains(err.Error(), "build-123, build-again") {
		t.Fatalf("expected ambiguous-builds error, got %v", err)
	}
}

// TestPromoteNoWaitResumeNoWait verifies a --no-wait re-run resumes the
// pending deployment without polling or flipping the alias.
func TestPromoteNoWaitResumeNoWait(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	buildCreates, fleetCreates, polled, flips := 0, 0, 0, 0
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.wait = false
	c.createResource = func(_ context.Context, r *cloud.Resource) error {
		buildCreates++
		r.Identifier = "build-1"
		return nil
	}
	c.createFleetAsync = func(_ context.Context, r *cloud.Resource) error {
		fleetCreates++
		r.Identifier = "fleet-1"
		return nil
	}
	c.fleetStatus = func(_ context.Context, id string) (cloud.FleetInfo, error) {
		polled++
		return cloud.FleetInfo{FleetID: id, Status: "ACTIVE"}, nil
	}
	c.updateResource = func(context.Context, *cloud.Resource) error { flips++; return nil }

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("first no-wait run: %v", err)
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("second no-wait run: %v", err)
	}
	if buildCreates != 1 || fleetCreates != 1 {
		t.Errorf("buildCreates = %d, fleetCreates = %d, want 1 and 1", buildCreates, fleetCreates)
	}
	if polled != 0 || flips != 0 {
		t.Errorf("polls = %d, flips = %d, want 0 and 0 (no-wait resume must not poll or flip)", polled, flips)
	}
	if !strings.Contains(out.String(), "Re-run without --no-wait to resume") {
		t.Errorf("expected no-wait resume guidance:\n%s", out.String())
	}
}

// TestPromoteDryRunPendingNote verifies a dry run names the pending promote
// and does not create anything.
func TestPromoteDryRunPendingNote(t *testing.T) {
	var out bytes.Buffer
	st := pendingState()
	c := newTestCmd(&out, st)
	c.dryRun = true
	builds := 0
	c.createResource = func(context.Context, *cloud.Resource) error { builds++; return nil }
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "Pending promote detected (build build-123, fleet fleet-pending)") {
		t.Errorf("expected pending note:\n%s", s)
	}
	if strings.Contains(s, "Run without --dry-run to register the build and create the fleet.") {
		t.Errorf("fresh-create line must not appear with a pending promote:\n%s", s)
	}
	if builds != 0 {
		t.Errorf("dry-run must not register builds, got %d", builds)
	}
}

// TestPromoteResumeConfirmRejected verifies a cancelled resume flips no
// alias and reports the cancellation.
func TestPromoteResumeConfirmRejected(t *testing.T) {
	var out bytes.Buffer
	st := pendingState()
	c := newTestCmd(&out, st)
	c.confirm = func(string) bool { return false }
	flipped := false
	c.updateResource = func(context.Context, *cloud.Resource) error { flipped = true; return nil }
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("confirm rejection should return nil: %v", err)
	}
	if flipped {
		t.Error("alias must not flip when resume is cancelled")
	}
	if !strings.Contains(out.String(), "cancelled") {
		t.Errorf("expected cancellation message:\n%s", out.String())
	}
}

// TestPromoteConfirmGateProceeds verifies the interactive confirm (no
// --yes) path proceeds to completion.
func TestPromoteConfirmGateProceeds(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.confirm = func(string) bool { return true }
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "live on fleet") {
		t.Errorf("expected completion after interactive confirm:\n%s", out.String())
	}
}

// TestPromoteFinalStateWriteFails verifies a failed final state write after
// the alias flip surfaces as an error.
func TestPromoteFinalStateWriteFails(t *testing.T) {
	var out bytes.Buffer
	st := seededState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.fleetStatus = func(_ context.Context, id string) (cloud.FleetInfo, error) {
		return cloud.FleetInfo{FleetID: id, Status: "ACTIVE"}, nil
	}
	calls := 0
	c.writeState = func(*fabricastate.State) error {
		calls++
		if calls == 3 {
			return errors.New("disk full")
		}
		return nil
	}
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "writing state") {
		t.Fatalf("expected final state-write error, got %v", err)
	}
}

// TestPromoteResumeRequiresGameLiftSupport verifies a resume on a provider
// without the GameLift seams fails before polling or flipping.
func TestPromoteResumeRequiresGameLiftSupport(t *testing.T) {
	var out bytes.Buffer
	st := pendingState()
	c := newTestCmd(&out, st)
	c.assumeYes = true
	c.updateResource = nil
	c.fleetStatus = nil
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when the provider lacks GameLift support on resume")
	}
}
