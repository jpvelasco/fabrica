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
	return globals.Runtime{Config: cfg, Provider: fakeProvider{}}
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
		sleep:       func(time.Duration) {},
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
	c.sleep = func(time.Duration) {}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout message: %v", err)
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

type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }
func (fakeProvider) Identity(context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-east-1", nil
}
func (fakeProvider) Resources() cloud.ResourceClient                         { return nil }
func (fakeProvider) CreateFleetAsync(context.Context, *cloud.Resource) error { return nil }
func (fakeProvider) FleetStatus(context.Context, string) (cloud.FleetInfo, error) {
	return cloud.FleetInfo{}, nil
}
func (fakeProvider) FleetEvents(context.Context, string) ([]cloud.FleetEvent, error) {
	return nil, nil
}
