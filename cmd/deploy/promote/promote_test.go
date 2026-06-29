package promote

import (
	"bytes"
	"context"
	"errors"
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
