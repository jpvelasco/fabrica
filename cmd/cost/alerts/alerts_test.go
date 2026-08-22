package alerts

import (
	"bytes"
	"encoding/json"
	"errors"
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

func TestListText(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Defaults()
	cfg.Cost.Budgets = []config.BudgetThreshold{{Scope: "total", Monthly: 400, WarnPct: 80}}
	c := listCommand{cfg: cfg, out: &out}
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "total") || !strings.Contains(out.String(), "400") {
		t.Fatalf("missing threshold:\n%s", out.String())
	}
}

func TestListEmpty(t *testing.T) {
	var out bytes.Buffer
	c := listCommand{cfg: config.Defaults(), out: &out}
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No budget thresholds configured") {
		t.Fatalf("expected empty-budgets message:\n%s", out.String())
	}
}

func TestSetUpsertsAndSaves(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Defaults()
	var saved *config.Config
	c := setCommand{
		cfg:     cfg,
		out:     &out,
		cfgPath: "fabrica.yaml",
		cfgSave: func(cc *config.Config, path string) error { saved = cc; return nil },
	}
	if err := c.run("perforce", 150, 0); err != nil {
		t.Fatal(err)
	}
	if saved == nil || len(saved.Cost.Budgets) != 1 {
		t.Fatalf("expected one saved budget, got %+v", saved)
	}
	if !strings.Contains(out.String(), "Next steps:") || !strings.Contains(out.String(), "fabrica cost alerts check") {
		t.Errorf("expected next-steps guidance:\n%s", out.String())
	}
	if saved.Cost.Budgets[0].Scope != "perforce" || saved.Cost.Budgets[0].Monthly != 150 {
		t.Fatalf("unexpected budget: %+v", saved.Cost.Budgets[0])
	}
	// upsert: setting the same scope again replaces, does not append.
	if err := c.run("perforce", 200, 90); err != nil {
		t.Fatal(err)
	}
	if len(saved.Cost.Budgets) != 1 || saved.Cost.Budgets[0].Monthly != 200 || saved.Cost.Budgets[0].WarnPct != 90 {
		t.Fatalf("upsert failed: %+v", saved.Cost.Budgets)
	}
}

func TestSetDryRunWritesNothing(t *testing.T) {
	var out bytes.Buffer
	saveCalled := false
	c := setCommand{
		cfg:     config.Defaults(),
		out:     &out,
		dryRun:  true,
		cfgPath: "fabrica.yaml",
		cfgSave: func(*config.Config, string) error { saveCalled = true; return nil },
	}
	if err := c.run("total", 500, 0); err != nil {
		t.Fatal(err)
	}
	if saveCalled {
		t.Fatal("dry-run must not write config")
	}
	if !strings.Contains(out.String(), "500") {
		t.Fatalf("dry-run should print the change:\n%s", out.String())
	}
}

func TestSetValidation(t *testing.T) {
	c := setCommand{cfg: config.Defaults(), out: &bytes.Buffer{}, cfgSave: func(*config.Config, string) error { return nil }}
	if err := c.run("perforce", 0, 0); err == nil {
		t.Fatal("expected error for monthly <= 0")
	}
	err := c.run("nonsense", 100, 0)
	if err == nil {
		t.Fatal("expected error for unknown scope")
	}
	// The error must enumerate the real scope list (derived from knownScopes),
	// including modules like lore and ddc.
	for _, want := range []string{"total", "lore", "ddc", "workstation", "deploy"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("scope error missing %q: %v", want, err)
		}
	}
}

// TestSetDDCScopeAccepted verifies the ddc module is a valid budget scope.
func TestSetDDCScopeAccepted(t *testing.T) {
	var out bytes.Buffer
	var saved *config.Config
	c := setCommand{
		cfg:     config.Defaults(),
		out:     &out,
		cfgPath: "fabrica.yaml",
		cfgSave: func(cfg *config.Config, _ string) error { saved = cfg; return nil },
	}
	if err := c.run("ddc", 300, 0); err != nil {
		t.Fatalf("ddc scope rejected: %v", err)
	}
	if saved == nil || len(saved.Cost.Budgets) != 1 || saved.Cost.Budgets[0].Scope != "ddc" {
		t.Fatalf("expected ddc budget saved, got %+v", saved)
	}
}

func TestCheckEvaluates(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Defaults()
	cfg.Cost.Budgets = []config.BudgetThreshold{{Scope: "perforce", Monthly: 10}} // way under -> OVER
	c := checkCommand{
		cfg:       cfg,
		costs:     cost.Global,
		out:       &out,
		readState: func() (*state.State, error) { return seededState(), nil },
	}
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "OVER") {
		t.Fatalf("expected OVER:\n%s", out.String())
	}
}

func TestCheckJSON(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Defaults()
	cfg.Cost.Budgets = []config.BudgetThreshold{{Scope: "perforce", Monthly: 10}}
	c := checkCommand{
		cfg:       cfg,
		costs:     cost.Global,
		jsonOut:   true,
		out:       &out,
		readState: func() (*state.State, error) { return seededState(), nil },
	}
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{`"scope"`, `"estimate"`, `"threshold"`, `"state"`, "perforce"} {
		if !strings.Contains(got, want) {
			t.Fatalf("JSON missing %q:\n%s", want, got)
		}
	}
}

func TestSetConfigWriteError(t *testing.T) {
	var out bytes.Buffer
	c := setCommand{
		cfg:     config.Defaults(),
		out:     &out,
		cfgPath: "fabrica.yaml",
		cfgSave: func(*config.Config, string) error { return errors.New("disk full") },
	}
	err := c.run("total", 500, 0)
	if err == nil {
		t.Fatal("expected error from config save")
	}
	if !strings.Contains(err.Error(), "saving config") {
		t.Fatalf("expected wrapped save error, got: %v", err)
	}
}

func TestCheckEmptyBudgets(t *testing.T) {
	var out bytes.Buffer
	c := checkCommand{
		cfg:       config.Defaults(),
		costs:     cost.Global,
		out:       &out,
		readState: func() (*state.State, error) { return seededState(), nil },
	}
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No budget thresholds configured") {
		t.Fatalf("expected empty-budgets message:\n%s", out.String())
	}
}

func TestCheckBudgetStates(t *testing.T) {
	// Perforce costs ~$180/mo (m5.large instance + EBS).
	// Set thresholds to test OK, WARN, and OVER.
	st := seededState()
	for _, tc := range []struct {
		name    string
		monthly float64
		state   string
	}{
		{"over", 10, "OVER"},
		{"warn", 200, "WARN"},
		{"ok", 500, "OK"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			cfg := config.Defaults()
			cfg.Cost.Budgets = []config.BudgetThreshold{{Scope: "perforce", Monthly: tc.monthly}}
			c := checkCommand{
				cfg:       cfg,
				costs:     cost.Global,
				out:       &out,
				readState: func() (*state.State, error) { return st, nil },
			}
			if err := c.run(); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out.String(), tc.state) {
				t.Fatalf("expected %s in:\n%s", tc.state, out.String())
			}
		})
	}
}

func TestCheckJSON_AllStates(t *testing.T) {
	st := seededState()
	cfg := config.Defaults()
	cfg.Cost.Budgets = []config.BudgetThreshold{
		{Scope: "perforce", Monthly: 10}, // OVER
		{Scope: "horde", Monthly: 10000}, // OK, no matching resources
	}
	var out bytes.Buffer
	c := checkCommand{
		cfg:       cfg,
		costs:     cost.Global,
		jsonOut:   true,
		out:       &out,
		readState: func() (*state.State, error) { return st, nil },
	}
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	var statuses []struct {
		Scope   string `json:"scope"`
		State   string `json:"state"`
		NoMatch bool   `json:"noMatch"`
	}
	if err := json.Unmarshal(out.Bytes(), &statuses); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if len(statuses) != 2 {
		t.Fatalf("expected 2 statuses, got %d", len(statuses))
	}
	// Sorted alphabetically: horde first, perforce second.
	if statuses[0].Scope != "horde" || statuses[0].State != "OK" || !statuses[0].NoMatch {
		t.Fatalf("expected horde/OK/NoMatch: %+v", statuses[0])
	}
	if statuses[1].Scope != "perforce" || statuses[1].State != "OVER" {
		t.Fatalf("expected perforce/OVER: %+v", statuses[1])
	}
}

func TestListJSON(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Defaults()
	cfg.Cost.Budgets = []config.BudgetThreshold{{Scope: "total", Monthly: 400, WarnPct: 80}}
	c := listCommand{cfg: cfg, jsonOut: true, out: &out}
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	var budgets []struct {
		Scope   string  `json:"scope"`
		Monthly float64 `json:"monthly"`
	}
	if err := json.Unmarshal(out.Bytes(), &budgets); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if len(budgets) != 1 || budgets[0].Scope != "total" {
		t.Fatalf("unexpected budget: %+v", budgets)
	}
}

func TestListJSONEmpty(t *testing.T) {
	var out bytes.Buffer
	c := listCommand{cfg: config.Defaults(), jsonOut: true, out: &out}
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	got := strings.TrimSpace(out.String())
	// Empty Budgets slice marshals to "null" (not "[]") since it's nil.
	if got != "null" {
		t.Fatalf("expected null for empty budgets, got:\n%s", got)
	}
}

func TestCheckReadStateError(t *testing.T) {
	c := checkCommand{
		cfg:       config.Defaults(),
		costs:     cost.Global,
		out:       &bytes.Buffer{},
		readState: func() (*state.State, error) { return nil, errors.New("state file missing") },
	}
	err := c.run()
	if err == nil {
		t.Fatal("expected error from readState")
	}
	if !strings.Contains(err.Error(), "reading state") {
		t.Fatalf("expected wrapped state error, got: %v", err)
	}
}

func TestSetInvalidMonthly(t *testing.T) {
	c := setCommand{cfg: config.Defaults(), out: &bytes.Buffer{}, cfgSave: func(*config.Config, string) error { return nil }}
	err := c.run("total", 0, 0)
	if err == nil {
		t.Fatal("expected error for monthly <= 0")
	}
	if !strings.Contains(err.Error(), "must be greater than 0") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetUnknownScope(t *testing.T) {
	c := setCommand{cfg: config.Defaults(), out: &bytes.Buffer{}, cfgSave: func(*config.Config, string) error { return nil }}
	err := c.run("nonsense", 100, 0)
	if err == nil {
		t.Fatal("expected error for unknown scope")
	}
	if !strings.Contains(err.Error(), "unknown scope") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSetDryRunWithWarnPct(t *testing.T) {
	var out bytes.Buffer
	saveCalled := false
	c := setCommand{
		cfg:     config.Defaults(),
		out:     &out,
		dryRun:  true,
		cfgPath: "fabrica.yaml",
		cfgSave: func(*config.Config, string) error { saveCalled = true; return nil },
	}
	if err := c.run("horde", 300, 90); err != nil {
		t.Fatal(err)
	}
	if saveCalled {
		t.Fatal("dry-run must not write config")
	}
	s := out.String()
	if !strings.Contains(s, "300") || !strings.Contains(s, "90%") || !strings.Contains(s, "Dry run") {
		t.Fatalf("dry-run output missing expected content:\n%s", s)
	}
}

func TestListTextWithWarnPct(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Defaults()
	cfg.Cost.Budgets = []config.BudgetThreshold{{Scope: "ci", Monthly: 100, WarnPct: 95}}
	c := listCommand{cfg: cfg, out: &out}
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "ci") || !strings.Contains(s, "95%") {
		t.Fatalf("expected custom warn pct:\n%s", s)
	}
}

func TestListTextDefaultWarnPct(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Defaults()
	cfg.Cost.Budgets = []config.BudgetThreshold{{Scope: "deploy", Monthly: 200, WarnPct: 0}}
	c := listCommand{cfg: cfg, out: &out}
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "80%") {
		t.Fatalf("expected default 80%% warn:\n%s", s)
	}
}

func TestCheckWithWarnPct(t *testing.T) {
	st := seededState()
	cfg := config.Defaults()
	cfg.Cost.Budgets = []config.BudgetThreshold{{Scope: "perforce", Monthly: 1000, WarnPct: 50}}
	var out bytes.Buffer
	c := checkCommand{
		cfg:       cfg,
		costs:     cost.Global,
		out:       &out,
		readState: func() (*state.State, error) { return st, nil },
	}
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	// With perforce ~$180 and threshold $1000 at 50% warn ($500), estimate $180 < $500, so OK.
	if !strings.Contains(s, "OK") {
		t.Fatalf("expected OK:\n%s", s)
	}
}
