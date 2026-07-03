package report

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
		Name:   "perforce",
		Status: "ready",
		Resources: []state.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-1"},
			{TypeName: "AWS::EC2::Volume", Identifier: "vol-1"},
		},
	}}
	return st
}

func newTestCommand(out *bytes.Buffer, jsonOut bool) command {
	return command{
		cfg:       config.Defaults(),
		costs:     cost.Global,
		jsonOut:   jsonOut,
		out:       out,
		readState: func() (*state.State, error) { return seededState(), nil },
	}
}

func TestReportText(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, false)
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"perforce", "Total", "Confidence", "fabrica.yaml"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
}

func TestReportJSON(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, true)
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Total   float64 `json:"total"`
		Modules []struct {
			Name string `json:"name"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if payload.Total <= 0 || len(payload.Modules) != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}
