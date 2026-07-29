package promote_test

import (
	"bytes"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/deploy/promote"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

// buildTestRoot constructs a minimal root command that mirrors the production
// flag hierarchy: --dry-run, --yes, and --json are persistent flags on root.
func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(promote.New(runtimeSource, optionsSource, out))
	return root
}

// runPromote builds the command tree, sets args, and executes.
func runPromote(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"promote"}, args...)...)
}

// newTestRuntime returns a RuntimeSource with a given provider.
func newTestRuntime(provider cloud.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.Deploy.BuildBucket = "deploy-builds"
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

// deployStateJSON returns a JSON string with deploy module provisioned (role + alias).
func deployStateJSON() string {
	return testutil.NewProvisionedStateJSON(testutil.StateModule{
		Name: "deploy", Version: "fabrica-deploy", Status: "ready",
		Resources: []testutil.StateResource{
			{TypeName: "AWS::IAM::Role", Identifier: "fabrica-deploy-gamelift"},
			{TypeName: "AWS::GameLift::Alias", Identifier: "alias-1"},
		},
	})
}

// TestPromoteCobraNotProvisioned verifies clean message when deploy is not set up.
func TestPromoteCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := runPromote(t, newTestRuntime(&testutil.GameLiftProvider{}), "v1.0.0")
	if err == nil {
		t.Fatal("expected error when deploy not provisioned")
	}
	testutil.AssertContains(t, err.Error(), "deploy is not set up")
}

// TestPromoteCobraDryRunShowsPlan verifies --dry-run shows the plan without AWS calls.
func TestPromoteCobraDryRunShowsPlan(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, deployStateJSON())

	provider := &testutil.GameLiftProvider{}
	got, err := runPromote(t, newTestRuntime(provider), "v1.0.0", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "dry run")
	testutil.AssertContains(t, got, "v1.0.0")
	testutil.AssertContains(t, got, "Cost estimate")
	if provider.CreateFleetAsyncCalls > 0 || provider.CreateCalls > 0 {
		t.Errorf("dry-run should not make AWS calls: createResource=%d createFleetAsync=%d", provider.CreateCalls, provider.CreateFleetAsyncCalls)
	}
}

// TestPromoteCobraDryRunShowsResources verifies build version and S3 path appear in dry-run output.
func TestPromoteCobraDryRunShowsResources(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, deployStateJSON())

	got, err := runPromote(t, newTestRuntime(&testutil.GameLiftProvider{}), "v1.2.3", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "v1.2.3")
	testutil.AssertContains(t, got, "s3://deploy-builds")
}

// TestPromoteCobraYesFlagWithNoWait verifies --yes --no-wait skips confirmation and avoids polling.
func TestPromoteCobraYesFlagWithNoWait(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, deployStateJSON())

	provider := &testutil.GameLiftProvider{}
	got, err := runPromote(t, newTestRuntime(provider), "v1.0.0", "--yes", "--no-wait")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.CreateCalls != 1 {
		t.Errorf("expected 1 createResource call for build, got %d", provider.CreateCalls)
	}
	if provider.CreateFleetAsyncCalls != 1 {
		t.Errorf("expected 1 createFleetAsync call, got %d", provider.CreateFleetAsyncCalls)
	}
	if provider.FleetStatusCalls > 0 {
		t.Errorf("--no-wait should not poll fleet status, but made %d calls", provider.FleetStatusCalls)
	}
	if provider.UpdateCalls > 0 {
		t.Errorf("--no-wait should not flip alias, but made %d update calls", provider.UpdateCalls)
	}
	testutil.AssertContains(t, got, "Fleet creation started")
}

// TestPromoteCobraJSONDryRun verifies --json --dry-run work together.
func TestPromoteCobraJSONDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, deployStateJSON())

	_, err := runPromote(t, newTestRuntime(&testutil.GameLiftProvider{}), "v1.0.0", "--json", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestPromoteCobraFakeProviderImplementsInterfaces verifies the fake satisfies both interfaces.
func TestPromoteCobraFakeProviderImplementsInterfaces(t *testing.T) {
	var p cloud.Provider = &testutil.GameLiftProvider{}
	if _, ok := p.(cloud.GameLiftManager); !ok {
		t.Fatal("GameLiftProvider does not implement cloud.GameLiftManager")
	}
}
