package rollback

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
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
		runtime:        globals.Runtime{Config: config.Defaults(), Provider: fakeProvider{}},
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
