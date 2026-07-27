package status

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func newTestCmd(out *bytes.Buffer, st *fabricastate.State) *command {
	return &command{
		runtime:   globals.Runtime{Config: config.Defaults(), Provider: fakeProvider{}},
		out:       out,
		readState: func() (*fabricastate.State, error) { return st, nil },
		fleetStatus: func(_ context.Context, id string) (cloud.FleetInfo, error) {
			return cloud.FleetInfo{FleetID: id, Status: "ACTIVE"}, nil
		},
		fleetEvents: func(context.Context, string) ([]cloud.FleetEvent, error) { return nil, nil },
	}
}

func deployState() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("deploy", "v2", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::GameLift::Alias", Identifier: "alias-1"},
		{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-new", Properties: map[string]string{"role": "active", "buildVersion": "v2"}},
		{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-old", Properties: map[string]string{"role": "superseded", "buildVersion": "v1"}},
	})
	return st
}

func TestStatusNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCmd(&out, st)
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "not set up") {
		t.Errorf("expected not-set-up message:\n%s", out.String())
	}
}

func TestStatusShowsActiveAndRollbackCandidate(t *testing.T) {
	var out bytes.Buffer
	c := newTestCmd(&out, deployState())
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "fleet-new") || !strings.Contains(s, "active") {
		t.Errorf("expected active fleet:\n%s", s)
	}
	if !strings.Contains(s, "fleet-old") || !strings.Contains(strings.ToLower(s), "rollback") {
		t.Errorf("expected rollback candidate labeled:\n%s", s)
	}
}

func TestStatusSummaryAndSymbols(t *testing.T) {
	var out bytes.Buffer
	c := newTestCmd(&out, deployState())
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	// Summary headline names the active fleet and the rollback count.
	if !strings.Contains(s, "active fleet fleet-new") {
		t.Errorf("expected summary line naming active fleet:\n%s", s)
	}
	if !strings.Contains(s, "rollback candidate(s)") {
		t.Errorf("expected summary to mention rollback candidate count:\n%s", s)
	}
	// ACTIVE fleets render the [OK] indicator; rollback targets are marked.
	if !strings.Contains(s, "[OK]") {
		t.Errorf("expected [OK] status symbol for ACTIVE fleet:\n%s", s)
	}
	if !strings.Contains(s, "rollback target") {
		t.Errorf("expected rollback target marker:\n%s", s)
	}
}

func TestStatusSymbolMapping(t *testing.T) {
	cases := map[string]string{
		"ACTIVE":     "[OK]  ",
		"BUILDING":   "[....]",
		"ACTIVATING": "[....]",
		"ERROR":      "[FAIL]",
		"TERMINATED": "[FAIL]",
		"WAT":        "[????]",
	}
	for status, want := range cases {
		if got := fleetSymbol(status); got != want {
			t.Errorf("fleetSymbol(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestStatusJSON(t *testing.T) {
	var out bytes.Buffer
	c := newTestCmd(&out, deployState())
	c.jsonOut = true
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "\"activeFleet\"") || !strings.Contains(s, "fleet-new") {
		t.Errorf("expected JSON with activeFleet:\n%s", s)
	}
}

func TestOrDash(t *testing.T) {
	if got := orDash(""); got != "(none)" {
		t.Errorf("orDash(\"\") = %q, want \"(none)\"", got)
	}
	if got := orDash("fleet-123"); got != "fleet-123" {
		t.Errorf("orDash(\"fleet-123\") = %q, want \"fleet-123\"", got)
	}
}

func TestLiveStatusNoProvider(t *testing.T) {
	var out bytes.Buffer
	c := &command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &out,
		readState: func() (*fabricastate.State, error) { return fabricastate.NewState("123456789012", "us-east-1"), nil },
	}
	if got := c.liveStatus(context.Background(), "fleet-1"); got != "unknown (no provider)" {
		t.Errorf("liveStatus with nil seam = %q, want \"unknown (no provider)\"", got)
	}
}

func TestLiveStatusError(t *testing.T) {
	var out bytes.Buffer
	c := &command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &out,
		readState: func() (*fabricastate.State, error) { return fabricastate.NewState("123456789012", "us-east-1"), nil },
		fleetStatus: func(context.Context, string) (cloud.FleetInfo, error) {
			return cloud.FleetInfo{}, fmt.Errorf("connection refused")
		},
	}
	if got := c.liveStatus(context.Background(), "fleet-1"); got != "unknown" {
		t.Errorf("liveStatus with error = %q, want \"unknown\"", got)
	}
}

func TestLiveStatusActive(t *testing.T) {
	var out bytes.Buffer
	c := &command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &out,
		readState: func() (*fabricastate.State, error) { return fabricastate.NewState("123456789012", "us-east-1"), nil },
		fleetStatus: func(context.Context, string) (cloud.FleetInfo, error) {
			return cloud.FleetInfo{FleetID: "fleet-1", Status: "ACTIVE"}, nil
		},
	}
	if got := c.liveStatus(context.Background(), "fleet-1"); got != "ACTIVE" {
		t.Errorf("liveStatus = %q, want \"ACTIVE\"", got)
	}
}

func TestSummaryLineNotSetUp(t *testing.T) {
	got := summaryLine("", nil, nil)
	if got != "deploy not fully set up — run 'fabrica deploy setup'" {
		t.Errorf("summaryLine empty alias = %q", got)
	}
}

func TestSummaryLineNoFleet(t *testing.T) {
	got := summaryLine("alias-1", nil, nil)
	if got != "alias ready • no fleet deployed yet" {
		t.Errorf("summaryLine no fleet = %q", got)
	}
}

func TestSummaryLineWithFleet(t *testing.T) {
	active := &fleetJSON{FleetID: "fleet-1", LiveStatus: "ACTIVE"}
	got := summaryLine("alias-1", active, nil)
	if got != "alias ready • active fleet fleet-1 (ACTIVE)" {
		t.Errorf("summaryLine with fleet = %q", got)
	}
}

func TestSummaryLineWithRollback(t *testing.T) {
	active := &fleetJSON{FleetID: "fleet-2", LiveStatus: "ACTIVE"}
	candidates := []fleetJSON{{FleetID: "fleet-1", LiveStatus: "ACTIVE"}}
	got := summaryLine("alias-1", active, candidates)
	if got != "alias ready • active fleet fleet-2 (ACTIVE) • 1 rollback candidate(s)" {
		t.Errorf("summaryLine with rollback = %q", got)
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
