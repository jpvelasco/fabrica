package alerts_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/cost/alerts"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

// buildTestRoot constructs a minimal root command that mirrors the production
// flag hierarchy: --dry-run, --yes, and --json are persistent flags on root.
func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, opts := testutil.BuildTestRoot(out)
	optionsSource := func() globals.Options { return *opts }
	root.AddCommand(alerts.New(runtimeSource, optionsSource, out))
	return root
}

// runAlertsCmd builds the command tree, sets args, and executes.
func runAlertsCmd(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"alerts"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

// newTestRuntime returns a RuntimeSource with a given config and nil provider
// (cost commands are offline and do not use the provider).
func newTestRuntime(cfg *config.Config, cfgPath string) globals.RuntimeSource {
	rt := globals.Runtime{Config: cfg, Provider: nil, ConfigPath: cfgPath}
	return func() (globals.Runtime, error) { return rt, nil }
}

// seededState returns a test state with perforce module.
func seededState() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.Modules = []fabricastate.ModuleState{{
		Name:   "perforce",
		Status: "ready",
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-1"},
			{TypeName: "AWS::EC2::Volume", Identifier: "vol-1"},
		},
	}}
	return st
}

// writeStateFile writes state to the standard .fabrica/state.json location.
// Caller should have already chdir'd to the target directory.
func writeStateFile(t *testing.T, st *fabricastate.State) {
	t.Helper()
	if err := fabricastate.WriteState(st); err != nil {
		t.Fatal(err)
	}
}

// assertContains checks that s contains substr.
func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	testutil.AssertContains(t, s, substr)
}

// assertNotContains checks that s does not contain substr.
func assertNotContains(t *testing.T, s, substr string) {
	t.Helper()
	if strings.Contains(s, substr) {
		t.Fatalf("%q should not contain %q", s, substr)
	}
}

// TestAlertsListEmpty verifies "alerts list" with no budgets.
func TestAlertsListEmpty(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Defaults()
	got, err := runAlertsCmd(t, newTestRuntime(cfg, "fabrica.yaml"), "list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "No budget thresholds configured")
}

// TestAlertsListWithBudgets verifies "alerts list" displays configured budgets.
func TestAlertsListWithBudgets(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Defaults()
	cfg.Cost.Budgets = []config.BudgetThreshold{
		{Scope: "total", Monthly: 500, WarnPct: 80},
		{Scope: "perforce", Monthly: 200, WarnPct: 75},
	}
	got, err := runAlertsCmd(t, newTestRuntime(cfg, "fabrica.yaml"), "list")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "Configured budget thresholds:")
	assertContains(t, got, "total")
	assertContains(t, got, "500")
	assertContains(t, got, "perforce")
	assertContains(t, got, "200")
}

// TestAlertsListJSON verifies "alerts list --json" outputs JSON.
func TestAlertsListJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Defaults()
	cfg.Cost.Budgets = []config.BudgetThreshold{
		{Scope: "total", Monthly: 400, WarnPct: 85},
	}
	got, err := runAlertsCmd(t, newTestRuntime(cfg, "fabrica.yaml"), "list", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var budgets []config.BudgetThreshold
	if err := json.Unmarshal([]byte(got), &budgets); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if len(budgets) != 1 || budgets[0].Scope != "total" || budgets[0].Monthly != 400 {
		t.Fatalf("unexpected budgets: %+v", budgets)
	}
}

// TestAlertsSetDryRunDoesNotWrite verifies --dry-run shows change without writing.
func TestAlertsSetDryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := config.Defaults()
	cfgPath := dir + "/fabrica.yaml"
	got, err := runAlertsCmd(t, newTestRuntime(cfg, cfgPath), "set", "total", "500", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "Would set budget")
	assertContains(t, got, "500")
	assertContains(t, got, "Dry run — fabrica.yaml not modified")
	// Verify config file was not written
	_, err = os.Stat(cfgPath)
	if err == nil {
		t.Fatal("config file should not exist after --dry-run")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected error checking config file: %v", err)
	}
}

// TestAlertsSetWritesAndSaves verifies "set" creates budget and saves fabrica.yaml.
func TestAlertsSetWritesAndSaves(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// Create initial fabrica.yaml so Save() has something to work with
	cfg := config.Defaults()
	cfgPath := dir + "/fabrica.yaml"
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	got, err := runAlertsCmd(t, newTestRuntime(cfg, cfgPath), "set", "perforce", "250")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "Set budget: perforce = $250.00")
	assertContains(t, got, "Next steps:")
	assertContains(t, got, "fabrica cost alerts check")
	// Verify config file exists and is readable
	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to reload config: %v", err)
	}
	if len(reloaded.Cost.Budgets) != 1 || reloaded.Cost.Budgets[0].Scope != "perforce" {
		t.Fatalf("expected perforce budget in reloaded config, got %+v", reloaded.Cost.Budgets)
	}
	if reloaded.Cost.Budgets[0].Monthly != 250 {
		t.Fatalf("expected monthly=250, got %v", reloaded.Cost.Budgets[0].Monthly)
	}
}

// TestAlertsSetWithWarnPct verifies --warn-pct is set correctly.
func TestAlertsSetWithWarnPct(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := config.Defaults()
	cfgPath := dir + "/fabrica.yaml"
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	got, err := runAlertsCmd(t, newTestRuntime(cfg, cfgPath), "set", "horde", "300", "--warn-pct", "90")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "Set budget: horde = $300.00")
	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("failed to reload config: %v", err)
	}
	if reloaded.Cost.Budgets[0].WarnPct != 90 {
		t.Fatalf("expected WarnPct=90, got %v", reloaded.Cost.Budgets[0].WarnPct)
	}
}

// TestAlertsSetValidationMonthlyPositive verifies monthly > 0 is required.
func TestAlertsSetValidationMonthlyPositive(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := config.Defaults()
	_, err := runAlertsCmd(t, newTestRuntime(cfg, dir+"/fabrica.yaml"), "set", "total", "0")
	if err == nil {
		t.Fatal("expected error for monthly <= 0")
	}
}

// TestAlertsSetValidationKnownScope verifies scope must be known.
func TestAlertsSetValidationKnownScope(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := config.Defaults()
	_, err := runAlertsCmd(t, newTestRuntime(cfg, dir+"/fabrica.yaml"), "set", "unknown_scope", "100")
	if err == nil {
		t.Fatal("expected error for unknown scope")
	}
}

// TestAlertsCheckEmpty verifies "check" with no budgets.
func TestAlertsCheckEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, seededState())
	cfg := config.Defaults()
	got, err := runAlertsCmd(t, newTestRuntime(cfg, dir+"/fabrica.yaml"), "check")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "No budget thresholds configured")
}

// TestAlertsCheckWithBudgets verifies "check" evaluates budgets against estimated cost.
func TestAlertsCheckWithBudgets(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, seededState())
	cfg := config.Defaults()
	// Set a tight budget (perforce costs ~$75/month at default m5.large)
	cfg.Cost.Budgets = []config.BudgetThreshold{
		{Scope: "perforce", Monthly: 10}, // way under the estimate
	}
	got, err := runAlertsCmd(t, newTestRuntime(cfg, dir+"/fabrica.yaml"), "check")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "perforce")
}

// TestAlertsCheckJSON verifies "check --json" outputs structured JSON.
func TestAlertsCheckJSON(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, seededState())
	cfg := config.Defaults()
	cfg.Cost.Budgets = []config.BudgetThreshold{
		{Scope: "perforce", Monthly: 50},
	}
	got, err := runAlertsCmd(t, newTestRuntime(cfg, dir+"/fabrica.yaml"), "check", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var statuses []struct {
		Scope     string  `json:"scope"`
		Estimate  float64 `json:"estimate"`
		Threshold float64 `json:"threshold"`
		WarnPct   int     `json:"warnPct"`
		State     string  `json:"state"`
	}
	if err := json.Unmarshal([]byte(got), &statuses); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if len(statuses) == 0 {
		t.Fatalf("expected at least one status in JSON")
	}
	if statuses[0].Scope != "perforce" {
		t.Fatalf("expected scope=perforce, got %v", statuses[0].Scope)
	}
}

// TestAlertsSetUpserts verifies setting the same scope replaces, not appends.
func TestAlertsSetUpserts(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := config.Defaults()
	cfgPath := dir + "/fabrica.yaml"
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	// First set
	_, err := runAlertsCmd(t, newTestRuntime(cfg, cfgPath), "set", "total", "100")
	if err != nil {
		t.Fatalf("first set failed: %v", err)
	}
	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Cost.Budgets) != 1 {
		t.Fatalf("expected 1 budget after first set, got %d", len(reloaded.Cost.Budgets))
	}
	// Second set (same scope, different value)
	_, err = runAlertsCmd(t, newTestRuntime(reloaded, cfgPath), "set", "total", "200")
	if err != nil {
		t.Fatalf("second set failed: %v", err)
	}
	reloaded, err = config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Cost.Budgets) != 1 {
		t.Fatalf("expected 1 budget after upsert, got %d", len(reloaded.Cost.Budgets))
	}
	if reloaded.Cost.Budgets[0].Monthly != 200 {
		t.Fatalf("expected monthly=200 after upsert, got %v", reloaded.Cost.Budgets[0].Monthly)
	}
}

// TestAlertsRuntimeError verifies runtimeSource errors surface as command errors.
func TestAlertsRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config load failed")
	}
	_, err := runAlertsCmd(t, src, "list")
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// TestAlertsCobraParentCommand verifies the parent "alerts" command structure.
func TestAlertsCobraParentCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Defaults()
	var out bytes.Buffer
	root := buildTestRoot(newTestRuntime(cfg, "fabrica.yaml"), &out)
	root.SetArgs([]string{"alerts", "-h"})
	err := root.ExecuteContext(context.Background())
	// Help command returns no error in cobra
	if err != nil && !strings.Contains(err.Error(), "help") {
		t.Fatalf("help invocation unexpected error: %v", err)
	}
	output := out.String()
	assertContains(t, output, "Manage local budget thresholds")
}

// TestAlertsListJSONEmpty verifies "alerts list --json" with no budgets is valid JSON.
func TestAlertsListJSONEmpty(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Defaults()
	got, err := runAlertsCmd(t, newTestRuntime(cfg, "fabrica.yaml"), "list", "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var budgets []config.BudgetThreshold
	if err := json.Unmarshal([]byte(got), &budgets); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	// Empty list should parse as an array (may be nil or empty)
	if len(budgets) != 0 {
		t.Fatalf("expected empty budgets, got %d", len(budgets))
	}
}

// TestAlertsCheckNilProvider verifies cost commands work with nil provider.
func TestAlertsCheckNilProvider(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, seededState())
	cfg := config.Defaults()
	cfg.Cost.Budgets = []config.BudgetThreshold{
		{Scope: "total", Monthly: 100},
	}
	rt := globals.Runtime{Config: cfg, Provider: nil, ConfigPath: dir + "/fabrica.yaml"}
	runtimeSrc := func() (globals.Runtime, error) { return rt, nil }
	got, err := runAlertsCmd(t, runtimeSrc, "check")
	if err != nil {
		t.Fatalf("unexpected error with nil provider: %v", err)
	}
	assertNotContains(t, got, "error")
}
