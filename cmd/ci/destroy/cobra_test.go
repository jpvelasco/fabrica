package destroy_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ci/destroy"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/spf13/cobra"
)

// buildTestRoot constructs a minimal root command that mirrors the production
// flag hierarchy: --dry-run, --yes, and --json are persistent flags on root.
func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return root
}

// runCIDestroy builds the command tree, sets args, and executes.
func runCIDestroy(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"destroy"}, args...)...)
}

// ciStateJSON returns a JSON string with CI module provisioned.
func ciStateJSON() string {
	return testutil.NewProvisionedStateJSON(testutil.StateModule{
		Name: "ci", Version: "fabrica-ci", Status: "ready",
		Resources: []testutil.StateResource{
			{TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci"},
			{TypeName: "AWS::IAM::Role", Identifier: "fabrica-ci-codebuild"},
		},
	})
}

// TestCIDestroyCobraNotProvisioned verifies clean message when no CI state exists.
func TestCIDestroyCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runCIDestroy(t, testutil.NewTestRuntime(&testutil.CodeBuildProvider{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
}

// TestCIDestroyCobraDryRunNoDeleteCalls verifies --dry-run produces output without delete calls.
func TestCIDestroyCobraDryRunNoDeleteCalls(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, ciStateJSON())

	provider := &testutil.CodeBuildProvider{}
	got, err := runCIDestroy(t, testutil.NewTestRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "dry run")
	if provider.DeleteProjectCalls != 0 || provider.DeleteCalls != 0 {
		t.Errorf("dry-run made delete calls: project=%d role=%d", provider.DeleteProjectCalls, provider.DeleteCalls)
	}
}

// TestCIDestroyCobraDryRunShowsResources verifies resource IDs appear in dry-run output.
func TestCIDestroyCobraDryRunShowsResources(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, ciStateJSON())

	got, err := runCIDestroy(t, testutil.NewTestRuntime(&testutil.CodeBuildProvider{}), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "fabrica-ci")
	testutil.AssertContains(t, got, "fabrica-ci-codebuild")
}

// TestCIDestroyCobraYesFlagDestroysResources verifies --yes destroys without prompt.
func TestCIDestroyCobraYesFlagDestroysResources(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, ciStateJSON())

	provider := &testutil.CodeBuildProvider{}
	got, err := runCIDestroy(t, testutil.NewTestRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.DeleteProjectCalls != 1 {
		t.Errorf("expected 1 project delete call, got %d", provider.DeleteProjectCalls)
	}
	if provider.DeleteCalls != 1 {
		t.Errorf("expected 1 role delete call, got %d", provider.DeleteCalls)
	}
	testutil.AssertContains(t, got, "destroyed")
}

// TestCIDestroyCobraJSONNotProvisioned verifies --json output when not provisioned.
func TestCIDestroyCobraJSONNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := runCIDestroy(t, testutil.NewTestRuntime(&testutil.CodeBuildProvider{}), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCIDestroyCobraJSONDryRun verifies --json --dry-run work together.
func TestCIDestroyCobraJSONDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, ciStateJSON())

	_, err := runCIDestroy(t, testutil.NewTestRuntime(&testutil.CodeBuildProvider{}), "--json", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCIDestroyCobraJSONYes verifies --json --yes work together.
func TestCIDestroyCobraJSONYes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, ciStateJSON())

	provider := &testutil.CodeBuildProvider{}
	_, err := runCIDestroy(t, testutil.NewTestRuntime(provider), "--json", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.DeleteProjectCalls != 1 || provider.DeleteCalls != 1 {
		t.Fatalf("expected both deletes, got project=%d role=%d", provider.DeleteProjectCalls, provider.DeleteCalls)
	}
}

// TestCIDestroyCobraNilProvider verifies nil provider with no state exits cleanly.
func TestCIDestroyCobraNilProvider(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runCIDestroy(t, testutil.NewNilProviderRuntime())
	if err != nil {
		t.Fatalf("nil provider: unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
}

// TestCIDestroyCobraRuntimeError verifies runtimeSource errors surface as command errors.
func TestCIDestroyCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	_, err := runCIDestroy(t, src)
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// TestCICobraFakeProviderImplementsInterfaces verifies the fake provider satisfies all required interfaces.
func TestCICobraFakeProviderImplementsInterfaces(t *testing.T) {
	var p cloud.Provider = &testutil.CodeBuildProvider{}
	if _, ok := p.(cloud.CodeBuildRunner); !ok {
		t.Fatal("CodeBuildProvider does not implement cloud.CodeBuildRunner")
	}
}
