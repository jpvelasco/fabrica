package alerts

import (
	"bytes"
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
	if err := c.run("nonsense", 100, 0); err == nil {
		t.Fatal("expected error for unknown scope")
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
