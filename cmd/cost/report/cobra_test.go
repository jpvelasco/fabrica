package report_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/cost/report"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

// buildTestRoot constructs a minimal root command that mirrors the production
// flag hierarchy: --dry-run, --yes, and --json are persistent flags on root.
func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, opts := testutil.BuildTestRoot(out)
	optionsSource := func() globals.Options { return *opts }
	root.AddCommand(report.New(runtimeSource, optionsSource, out))
	return root
}

// runCostReport builds the command tree, sets args, and executes.
func runCostReport(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"report"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

// newTestRuntime returns a RuntimeSource with no provider (cost commands are offline).
func newTestRuntime(cfg *config.Config) globals.RuntimeSource {
	if cfg == nil {
		cfg = config.Defaults()
	}
	rt := globals.Runtime{Config: cfg, Provider: nil}
	return func() (globals.Runtime, error) { return rt, nil }
}

// reportStateJSON returns a JSON string with perforce module provisioned.
func reportStateJSON() string {
	return `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"perforce","version":"ami-123456","status":"ready","resources":[
			{"typeName":"AWS::EC2::Instance","identifier":"i-1234567890abcdef0"},
			{"typeName":"AWS::EC2::Volume","identifier":"vol-1234567890abcdef0"}
		]}]}`
}

// TestCostReportCobraText verifies text output with provisioned module.
func TestCostReportCobraText(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, reportStateJSON())

	got, err := runCostReport(t, newTestRuntime(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"perforce", "Total", "Confidence"} {
		testutil.AssertContains(t, got, want)
	}
}

// TestCostReportCobraTextEmpty verifies text output with no provisioned modules.
func TestCostReportCobraTextEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, `{"account":"123456789012","region":"us-east-1","modules":[]}`)

	got, err := runCostReport(t, newTestRuntime(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "No provisioned modules")
}

// TestCostReportCobraJSON verifies JSON output with provisioned module.
func TestCostReportCobraJSON(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, reportStateJSON())

	got, err := runCostReport(t, newTestRuntime(nil), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload struct {
		Total      float64 `json:"total"`
		Confidence string  `json:"confidence"`
		Modules    []struct {
			Name string `json:"name"`
		} `json:"modules"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if payload.Total <= 0 {
		t.Errorf("expected positive total, got %v", payload.Total)
	}
	if len(payload.Modules) != 1 {
		t.Errorf("expected 1 module, got %d", len(payload.Modules))
	}
	if payload.Modules[0].Name != "perforce" {
		t.Errorf("expected module name perforce, got %s", payload.Modules[0].Name)
	}
}

// TestCostReportCobraJSONEmpty verifies JSON output with no provisioned modules.
func TestCostReportCobraJSONEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, `{"account":"123456789012","region":"us-east-1","modules":[]}`)

	got, err := runCostReport(t, newTestRuntime(nil), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload struct {
		Total   float64 `json:"total"`
		Modules []struct {
			Name string `json:"name"`
		} `json:"modules"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if len(payload.Modules) != 0 {
		t.Errorf("expected 0 modules, got %d", len(payload.Modules))
	}
}

// TestCostReportCobraNoState verifies clean message when state file does not exist.
func TestCostReportCobraNoState(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runCostReport(t, newTestRuntime(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "No provisioned modules")
}

// TestCostReportCobraRuntimeError verifies runtimeSource errors surface as command errors.
func TestCostReportCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	t.Chdir(t.TempDir())
	_, err := runCostReport(t, src)
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// TestCostReportCobraConfidenceFieldPresent verifies confidence output in text mode.
func TestCostReportCobraConfidenceFieldPresent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, reportStateJSON())

	got, err := runCostReport(t, newTestRuntime(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "Confidence:")
}

// TestCostReportCobraJSONConfidenceFieldPresent verifies confidence field in JSON output.
func TestCostReportCobraJSONConfidenceFieldPresent(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, reportStateJSON())

	got, err := runCostReport(t, newTestRuntime(nil), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload struct {
		Confidence string `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(got), &payload); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, got)
	}
	if payload.Confidence == "" {
		t.Error("expected non-empty confidence field")
	}
}
