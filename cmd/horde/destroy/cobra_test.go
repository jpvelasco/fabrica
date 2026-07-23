package destroy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/horde/destroy"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, opts := testutil.BuildTestRoot(out)
	optionsSource := func() globals.Options { return *opts }
	root.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return root
}

func runDestroy(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"destroy"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

// TestDestroyCobraNotProvisioned verifies clean message when no state on disk.
func TestDestroyCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.CobraFakeProvider{}))
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

	provider := &testutil.CobraFakeProvider{}
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

	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.CobraFakeProvider{}), "--dry-run")
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

	provider := &testutil.CobraFakeProvider{}
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
	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.CobraFakeProvider{}), "--json")
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

	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.CobraFakeProvider{}), "--json", "--dry-run")
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

	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.CobraFakeProvider{}), "--json", "--yes")
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

// TestNewTeardownWiring verifies NewTeardown returns a Command with correct wiring.
func TestNewTeardownWiring(t *testing.T) {
	var out bytes.Buffer
	rt := globals.Runtime{Config: config.Defaults(), Provider: &testutil.CobraFakeProvider{}}
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
	if tc.Spec.ModuleName != "horde" {
		t.Errorf("module name = %q, want horde", tc.Spec.ModuleName)
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
	if tc.DeleteResource != nil || tc.GetResource != nil {
		t.Fatal("DeleteResource/GetResource must be nil when provider is nil")
	}
}

// ---- helpers ----

func provisionedStateJSON() string {
	return `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"horde","version":"ami-0abc123def456789","status":"provisioning","resources":[
			{"typeName":"AWS::EC2::SecurityGroup","identifier":"sg-cobra123"},
			{"typeName":"AWS::EC2::Instance","identifier":"i-cobra123"}
		]}]}`
}
