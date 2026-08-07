package driftcmd_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	driftcmd "github.com/jpvelasco/fabrica/cmd/drift"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(driftcmd.New(runtimeSource, optionsSource, out))
	return root
}

func runDrift(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"drift"}, args...)...)
}

// TestDriftCobraEmpty verifies a clean exit when no state exists.
func TestDriftCobraEmpty(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runDrift(t, testutil.NewNilProviderRuntime())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "No modules provisioned") {
		t.Errorf("expected 'No modules provisioned'; got:\n%s", got)
	}
}

// TestDriftCobraJSON verifies --json produces a parseable DriftReport.
func TestDriftCobraJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runDrift(t, testutil.NewNilProviderRuntime(), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(got), &report); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if _, ok := report["backend"]; !ok {
		t.Error("expected 'backend' field in JSON output")
	}
	if _, ok := report["modules"]; !ok {
		t.Error("expected 'modules' field in JSON output")
	}
}

// TestDriftCobraRuntimeError verifies runtimeSource errors surface as command errors.
func TestDriftCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	if _, err := runDrift(t, src); err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// TestDriftCobraWithModules verifies drift runs with provisioned modules and
// nil provider (all resources show as error since no resource client).
func TestDriftCobraWithModules(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	testutil.WriteStateFile(t, dir, testutil.NewProvisionedStateJSON(
		testutil.StateModule{
			Name:      "horde",
			Version:   "ami-fake",
			Status:    "ready",
			Resources: testutil.EC2Pair("sg-123", "i-123"),
		},
	))

	got, err := runDrift(t, testutil.NewNilProviderRuntime())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "horde") {
		t.Errorf("expected 'horde' in output; got:\n%s", got)
	}
	if !strings.Contains(got, "Summary") {
		t.Errorf("expected 'Summary' in output; got:\n%s", got)
	}
}

// TestDriftCobraWithModulesJSON verifies --json with provisioned modules.
func TestDriftCobraWithModulesJSON(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	testutil.WriteStateFile(t, dir, testutil.NewProvisionedStateJSON(
		testutil.StateModule{
			Name:      "horde",
			Version:   "ami-fake",
			Status:    "ready",
			Resources: testutil.EC2Pair("sg-123", "i-123"),
		},
	))

	got, err := runDrift(t, testutil.NewNilProviderRuntime(), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(got), &report); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	modules, ok := report["modules"].([]any)
	if !ok || len(modules) != 1 {
		t.Fatalf("expected 1 module in JSON, got %d", len(modules))
	}
}
