package forecast

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/state"
)

func seededState() *state.State {
	st := state.NewState("acct", "us-east-1")
	st.Modules = []state.ModuleState{{
		Name: "perforce", Status: "ready",
		Resources: []state.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-1"},
			{TypeName: "AWS::EC2::Volume", Identifier: "vol-1"},
		},
	}}
	return st
}

func newTestCommand(out *bytes.Buffer, days int, jsonOut bool) command {
	return command{
		cfg:       config.Defaults(),
		costs:     cost.Global,
		days:      days,
		jsonOut:   jsonOut,
		out:       out,
		readState: func() (*state.State, error) { return seededState(), nil },
	}
}

func TestForecastDefaultDays(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, 0, false) // 0 -> defaults to 30
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "30") {
		t.Fatalf("expected 30-day horizon:\n%s", out.String())
	}
}

func TestForecastJSON(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, 90, true)
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Days        int     `json:"days"`
		DailyBurn   float64 `json:"dailyBurn"`
		HorizonCost float64 `json:"horizonCost"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out.String())
	}
	if payload.Days != 90 || payload.DailyBurn <= 0 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}
