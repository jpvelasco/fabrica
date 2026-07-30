package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/costsource"
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

func TestReportRun_ReadStateError(t *testing.T) {
	var out bytes.Buffer
	c := command{
		cfg:       config.Defaults(),
		costs:     cost.Global,
		out:       &out,
		readState: func() (*state.State, error) { return nil, errors.New("state file missing") },
	}
	err := c.run()
	if err == nil {
		t.Fatal("expected error for readState failure")
	}
	if !strings.Contains(err.Error(), "reading state") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestReportRenderText_EmptyModules(t *testing.T) {
	var out bytes.Buffer
	c := command{
		cfg:       config.Defaults(),
		costs:     cost.Global,
		out:       &out,
		readState: func() (*state.State, error) { return state.NewState("acct", "us-east-1"), nil },
	}
	c.renderText(costsource.Breakdown{Confidence: cost.High})
	s := out.String()
	if !strings.Contains(s, "No provisioned modules found in state") {
		t.Fatalf("expected empty-modules message:\n%s", s)
	}
}

func TestReportRenderText_ErrorInResult(t *testing.T) {
	var out bytes.Buffer
	b := costsource.Breakdown{
		Confidence: cost.High,
		Modules: []costsource.ModuleCost{
			{
				Name:   "perforce",
				Status: "ready",
				Report: cost.Report{
					Results: []cost.EstimateResult{
						{Resource: cost.Resource{Name: "m7i.xlarge"}, Err: fmt.Errorf("no estimator")},
					},
				},
				Subtotal: 0,
			},
		},
	}
	c := command{out: &out}
	c.renderText(b)
	s := out.String()
	if !strings.Contains(s, "(no estimate)") {
		t.Fatalf("expected error marker:\n%s", s)
	}
}

func TestReportRenderText_Note(t *testing.T) {
	var out bytes.Buffer
	b := costsource.Breakdown{
		Confidence: cost.High,
		Modules: []costsource.ModuleCost{
			{
				Name:     "deploy",
				Status:   "ready",
				Report:   cost.Report{},
				Subtotal: 0,
				Note:     "setup only (no active fleet) — standing cost ~$0",
			},
		},
	}
	c := command{out: &out}
	c.renderText(b)
	s := out.String()
	if !strings.Contains(s, "setup only") {
		t.Fatalf("expected note in output:\n%s", s)
	}
}

func TestReportRenderJSON_MultipleModules(t *testing.T) {
	var out bytes.Buffer
	b := costsource.Breakdown{
		Confidence: cost.Medium,
		Total:      300,
		Modules: []costsource.ModuleCost{
			{Name: "perforce", Status: "ready", Subtotal: 100},
			{Name: "horde", Status: "provisioning", Subtotal: 200, Note: "building AMI"},
		},
	}
	c := command{out: &out}
	if err := c.renderJSON(b); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Total      float64 `json:"total"`
		Confidence string  `json:"confidence"`
		Modules    []struct {
			Name     string  `json:"name"`
			Status   string  `json:"status"`
			Subtotal float64 `json:"subtotal"`
			Note     string  `json:"note"`
		} `json:"modules"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if payload.Total != 300 {
		t.Fatalf("expected total 300, got %v", payload.Total)
	}
	if len(payload.Modules) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(payload.Modules))
	}
	if payload.Modules[1].Note != "building AMI" {
		t.Fatalf("expected note on second module: %q", payload.Modules[1].Note)
	}
	if payload.Confidence != "medium" {
		t.Fatalf("expected confidence medium, got %q", payload.Confidence)
	}
}
