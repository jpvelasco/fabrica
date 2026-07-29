package destroy_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/cmd/perforce/destroy"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return root
}

func runDestroy(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"destroy"}, args...)...)
}

// TestDestroyCobraNotProvisioned verifies clean message when no state on disk.
func TestDestroyCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.TestProvider{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
}

// TestDestroyCobraDryRunNoDeleteCalls verifies --dry-run produces output without calling delete.
func TestDestroyCobraDryRunNoDeleteCalls(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())

	provider := &testutil.TestProvider{}
	got, err := runDestroy(t, testutil.NewTestRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "dry run")
	if provider.DeleteCalls != 0 {
		t.Errorf("dry-run made %d delete calls, want 0", provider.DeleteCalls)
	}
}

// TestDestroyCobraDryRunShowsResources verifies resource IDs appear in dry-run output.
func TestDestroyCobraDryRunShowsResources(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())

	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.TestProvider{}), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "i-cobra123")
	testutil.AssertContains(t, got, "sg-cobra123")
}

// TestDestroyCobraYesFlagDestroysResources verifies --yes destroys without prompt.
func TestDestroyCobraYesFlagDestroysResources(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())

	provider := &testutil.TestProvider{}
	got, err := runDestroy(t, testutil.NewTestRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.DeleteCalls != 2 {
		t.Errorf("expected 2 delete calls, got %d", provider.DeleteCalls)
	}
	testutil.AssertContains(t, got, "destroyed")
}

// TestDestroyCobraJSONNotProvisioned verifies --json output when not provisioned.
func TestDestroyCobraJSONNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.TestProvider{}), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result teardown.Output
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if len(result.Destroyed) != 0 {
		t.Errorf("destroyed = %v, want empty", result.Destroyed)
	}
}

// TestDestroyCobraJSONDryRun verifies --json --dry-run outputs valid JSON with dryRun=true.
func TestDestroyCobraJSONDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())

	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.TestProvider{}), "--json", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result teardown.Output
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if !result.DryRun {
		t.Error("dryRun must be true")
	}
	if len(result.Destroyed) != 2 {
		t.Errorf("expected 2 in destroyed list for dry run, got %d", len(result.Destroyed))
	}
}

// TestDestroyCobraJSONYes verifies --json --yes output after successful destroy.
func TestDestroyCobraJSONYes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())

	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.TestProvider{}), "--json", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result teardown.Output
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if result.DryRun {
		t.Error("dryRun must be false")
	}
	if len(result.Destroyed) != 2 {
		t.Errorf("expected 2 destroyed, got %d: %v", len(result.Destroyed), result.Destroyed)
	}
}

// TestDestroyCobraNilProviderNoState verifies nil provider with no state exits cleanly.
func TestDestroyCobraNilProvider(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runDestroy(t, testutil.NewNilProviderRuntime())
	if err != nil {
		t.Fatalf("nil provider: unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
}

// TestDestroyCobraRuntimeError verifies runtimeSource errors surface as command errors.
func TestDestroyCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	_, err := runDestroy(t, src)
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// ---- helpers ----

func provisionedStateJSON() string {
	return testutil.NewProvisionedStateJSON(testutil.StateModule{
		Name: "perforce", Version: "2024.2", Status: "provisioning",
		Resources: testutil.EC2Pair("sg-cobra123", "i-cobra123"),
	})
}

// TestNewTeardownWiring verifies NewTeardown returns a Command with correct wiring.
func TestNewTeardownWiring(t *testing.T) {
	var out bytes.Buffer
	rt := globals.Runtime{Config: config.Defaults(), Provider: &testutil.TestProvider{}}
	tc := destroy.NewTeardown(rt, &out)
	if !tc.SkipConfirm || !tc.AssumeYes {
		t.Fatalf("SkipConfirm/AssumeYes must be true; got SkipConfirm=%v, AssumeYes=%v", tc.SkipConfirm, tc.AssumeYes)
	}
	if tc.ReadState == nil || tc.WriteState == nil || tc.DeleteResource == nil || tc.GetResource == nil {
		t.Fatal("provider seams must be wired when provider is non-nil")
	}
	if tc.Runtime.Provider == nil {
		t.Fatal("Runtime.Provider must be set")
	}
	if tc.Spec.ModuleName != "perforce" {
		t.Errorf("module name = %q, want perforce", tc.Spec.ModuleName)
	}
}

// TestNewTeardownNilProvider verifies NewTeardown handles nil provider gracefully.
func TestNewTeardownNilProvider(t *testing.T) {
	var out bytes.Buffer
	rt := globals.Runtime{Config: config.Defaults(), Provider: nil}
	tc := destroy.NewTeardown(rt, &out)
	if !tc.SkipConfirm || !tc.AssumeYes {
		t.Fatal("SkipConfirm/AssumeYes must be true even with nil provider")
	}
	if tc.ReadState == nil || tc.WriteState == nil {
		t.Fatal("ReadState and WriteState must always be wired")
	}
	// With nil provider, Delete/Get seams stay nil.
	if tc.DeleteResource != nil || tc.GetResource != nil {
		t.Fatal("DeleteResource/GetResource must be nil when provider is nil")
	}
}
