package promote_test

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/deploy/promote"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

// buildTestRoot constructs a minimal root command that mirrors the production
// flag hierarchy: --dry-run, --yes, and --json are persistent flags on root.
func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	var opts globals.Options
	root := &cobra.Command{
		Use:           "fabrica",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "")
	root.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "")
	root.PersistentFlags().BoolVarP(&opts.JSONOutput, "json", "j", false, "")
	root.SetOut(out)
	root.SetErr(out)

	optionsSource := func() globals.Options { return opts }
	root.AddCommand(promote.New(runtimeSource, optionsSource, out))
	return root
}

// runPromote builds the command tree, sets args, and executes.
func runPromote(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"promote"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
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
	return `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"deploy","version":"fabrica-deploy","status":"ready","resources":[
			{"typeName":"AWS::IAM::Role","identifier":"fabrica-deploy-gamelift"},
			{"typeName":"AWS::GameLift::Alias","identifier":"alias-1"}
		]}]}`
}

// writeStateFile writes deploy state to the standard .fabrica/state.json location.
func writeStateFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir+"/.fabrica", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/.fabrica/state.json", []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

// assertContains checks that s contains substr.
func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Fatalf("%q does not contain %q", s, substr)
}

// TestPromoteCobraNotProvisioned verifies clean message when deploy is not set up.
func TestPromoteCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := runPromote(t, newTestRuntime(&promoteCobraFakeProvider{}), "v1.0.0")
	if err == nil {
		t.Fatal("expected error when deploy not provisioned")
	}
	assertContains(t, err.Error(), "deploy is not set up")
}

// TestPromoteCobraDryRunShowsPlan verifies --dry-run shows the plan without AWS calls.
func TestPromoteCobraDryRunShowsPlan(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, deployStateJSON())

	provider := &promoteCobraFakeProvider{}
	got, err := runPromote(t, newTestRuntime(provider), "v1.0.0", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "dry run")
	assertContains(t, got, "v1.0.0")
	assertContains(t, got, "Cost estimate")
	if provider.createFleetAsyncCalls > 0 || provider.createResourceCalls > 0 {
		t.Errorf("dry-run should not make AWS calls: createResource=%d createFleetAsync=%d", provider.createResourceCalls, provider.createFleetAsyncCalls)
	}
}

// TestPromoteCobraDryRunShowsResources verifies build version and S3 path appear in dry-run output.
func TestPromoteCobraDryRunShowsResources(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, deployStateJSON())

	got, err := runPromote(t, newTestRuntime(&promoteCobraFakeProvider{}), "v1.2.3", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "v1.2.3")
	assertContains(t, got, "s3://deploy-builds")
}

// TestPromoteCobraYesFlagWithNoWait verifies --yes --no-wait skips confirmation and avoids polling.
func TestPromoteCobraYesFlagWithNoWait(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, deployStateJSON())

	provider := &promoteCobraFakeProvider{}
	got, err := runPromote(t, newTestRuntime(provider), "v1.0.0", "--yes", "--no-wait")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.createResourceCalls != 1 {
		t.Errorf("expected 1 createResource call for build, got %d", provider.createResourceCalls)
	}
	if provider.createFleetAsyncCalls != 1 {
		t.Errorf("expected 1 createFleetAsync call, got %d", provider.createFleetAsyncCalls)
	}
	if provider.fleetStatusCalls > 0 {
		t.Errorf("--no-wait should not poll fleet status, but made %d calls", provider.fleetStatusCalls)
	}
	if provider.updateResourceCalls > 0 {
		t.Errorf("--no-wait should not flip alias, but made %d update calls", provider.updateResourceCalls)
	}
	assertContains(t, got, "Fleet creation started")
}

// TestPromoteCobraJSONDryRun verifies --json --dry-run work together.
func TestPromoteCobraJSONDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, deployStateJSON())

	_, err := runPromote(t, newTestRuntime(&promoteCobraFakeProvider{}), "v1.0.0", "--json", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestPromoteCobraFakeProviderImplementsInterfaces verifies the fake satisfies both interfaces.
func TestPromoteCobraFakeProviderImplementsInterfaces(t *testing.T) {
	var p cloud.Provider = &promoteCobraFakeProvider{}
	if _, ok := p.(cloud.GameLiftManager); !ok {
		t.Fatal("promoteCobraFakeProvider does not implement cloud.GameLiftManager")
	}
}

// ---- promoteCobraFakeProvider ----

// promoteCobraFakeProvider is a minimal fake satisfying cloud.Provider and
// cloud.GameLiftManager for promote Cobra-layer tests.
type promoteCobraFakeProvider struct {
	createResourceCalls   int
	updateResourceCalls   int
	createFleetAsyncCalls int
	fleetStatusCalls      int
	fleetEventsCalls      int
}

func (f *promoteCobraFakeProvider) Name() string { return "fake" }

func (f *promoteCobraFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *promoteCobraFakeProvider) Resources() cloud.ResourceClient {
	return &promoteCobraFakeRC{provider: f}
}

// CreateFleetAsync implements cloud.GameLiftManager.
func (f *promoteCobraFakeProvider) CreateFleetAsync(_ context.Context, r *cloud.Resource) error {
	f.createFleetAsyncCalls++
	r.Identifier = "fleet-new"
	return nil
}

// FleetStatus implements cloud.GameLiftManager.
func (f *promoteCobraFakeProvider) FleetStatus(_ context.Context, _ string) (cloud.FleetInfo, error) {
	f.fleetStatusCalls++
	return cloud.FleetInfo{Status: "ACTIVE"}, nil
}

// FleetEvents implements cloud.GameLiftManager.
func (f *promoteCobraFakeProvider) FleetEvents(_ context.Context, _ string) ([]cloud.FleetEvent, error) {
	f.fleetEventsCalls++
	return nil, nil
}

type promoteCobraFakeRC struct {
	provider *promoteCobraFakeProvider
}

func (r *promoteCobraFakeRC) Create(_ context.Context, res *cloud.Resource) error {
	r.provider.createResourceCalls++
	res.Identifier = "build-123"
	return nil
}
func (r *promoteCobraFakeRC) Get(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *promoteCobraFakeRC) Update(_ context.Context, _ *cloud.Resource) error {
	r.provider.updateResourceCalls++
	return nil
}
func (r *promoteCobraFakeRC) Delete(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *promoteCobraFakeRC) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}
