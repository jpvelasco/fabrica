package destroy_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/cmd/lore/destroy"
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
	got, err := runDestroy(t, testutil.NewNilProviderRuntime())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
}

// TestNewTeardownWiring verifies NewTeardown returns a Command with correct wiring (nil provider).
func TestNewTeardownWiring(t *testing.T) {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: nil}
	tc := destroy.NewTeardown(rt, io.Discard)
	if tc.Spec.ModuleName != "lore" {
		t.Errorf("ModuleName = %q", tc.Spec.ModuleName)
	}
	if !tc.SkipConfirm {
		t.Error("SkipConfirm should be true for orchestrated teardown")
	}
}

// ---- helpers ----

func loreStateJSON() string {
	return testutil.NewProvisionedStateJSON(testutil.StateModule{
		Name: "lore", Version: "ami-0abc123", Status: "provisioning",
		Resources: testutil.EC2Pair("sg-lore123", "i-lore123"),
	})
}

// ---- Cobra tests with provider ----

// TestDestroyCobraDryRunWithProvider verifies --dry-run produces output without calling delete.
func TestDestroyCobraDryRunWithProvider(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, loreStateJSON())
	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.CobraFakeProvider{}), "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	testutil.AssertContains(t, got, "dry run")
	testutil.AssertContains(t, got, "i-lore123")
}

// TestDestroyCobraYesWithProvider verifies --yes destroys without prompt.
func TestDestroyCobraYesWithProvider(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, loreStateJSON())
	provider := &testutil.CobraFakeProvider{}
	_, err := runDestroy(t, testutil.NewTestRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("destroy --yes: %v", err)
	}
	if provider.DeleteCalls != 2 {
		t.Errorf("expected 2 delete calls, got %d", provider.DeleteCalls)
	}
}

// TestDestroyCobraJSONDryRunWithProvider verifies --json --dry-run outputs valid JSON with dryRun=true.
func TestDestroyCobraJSONDryRunWithProvider(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, loreStateJSON())
	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.CobraFakeProvider{}), "--json", "--dry-run")
	if err != nil {
		t.Fatalf("json dry-run: %v", err)
	}
	var result teardown.Output
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if !result.DryRun {
		t.Error("dryRun must be true")
	}
	if len(result.Destroyed) != 2 {
		t.Errorf("expected 2 resources in dry run, got %d", len(result.Destroyed))
	}
}

// TestDestroyCobraJSONYesWithProvider verifies --json --yes output after successful destroy.
func TestDestroyCobraJSONYesWithProvider(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, loreStateJSON())
	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.CobraFakeProvider{}), "--json", "--yes")
	if err != nil {
		t.Fatalf("json yes: %v", err)
	}
	var result teardown.Output
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if result.DryRun {
		t.Error("dryRun must be false")
	}
}

// TestDestroyCobraRuntimeError verifies runtimeSource errors surface as command errors.
func TestDestroyCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	_, err := runDestroy(t, src, "--yes")
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// ---- NewTeardown with provider ----

// TestNewTeardownWiringWithProvider verifies NewTeardown returns a Command with correct wiring (non-nil provider).
func TestNewTeardownWiringWithProvider(t *testing.T) {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: &testutil.CobraFakeProvider{}}
	tc := destroy.NewTeardown(rt, io.Discard)
	if !tc.SkipConfirm || !tc.AssumeYes {
		t.Fatalf("SkipConfirm/AssumeYes must be true; got SkipConfirm=%v, AssumeYes=%v", tc.SkipConfirm, tc.AssumeYes)
	}
	if tc.DeleteResource == nil {
		t.Error("DeleteResource must be wired when provider is non-nil")
	}
	if tc.GetResource == nil {
		t.Error("GetResource must be wired when provider is non-nil")
	}
	if tc.Spec.ModuleName != "lore" {
		t.Errorf("module name = %q, want lore", tc.Spec.ModuleName)
	}
}
