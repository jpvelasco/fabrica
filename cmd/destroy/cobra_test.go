package destroy_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/destroy"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

// buildTestRoot constructs a minimal root command that mirrors the production
// flag hierarchy: --dry-run and --yes are persistent flags on root, inherited
// by the destroy subcommand.
func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, opts := testutil.BuildTestRoot(out)
	optionsSource := func() globals.Options { return *opts }
	root.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return root
}

// runDestroy builds the command tree, sets args, and executes. Returns
// captured output and any command error.
func runDestroy(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"destroy"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

// newCobraTestRuntime returns a RuntimeSource backed by the given provider with
// a pre-configured config (bucket and table names known to all Cobra tests).
func newCobraTestRuntime(provider cloud.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.State.Table = "fabrica-locks-test"
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

// newNilProviderRuntime returns a RuntimeSource with a nil provider
// (simulates "no infrastructure configured").
func newNilProviderRuntime() globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.State.Table = "fabrica-locks-test"
	rt := globals.Runtime{Config: cfg, Provider: nil}
	return func() (globals.Runtime, error) { return rt, nil }
}

// TestDestroyCobraNoAllFlag verifies that omitting --all prints the usage
// hint and exits cleanly.
func TestDestroyCobraNoAllFlag(t *testing.T) {
	got, err := runDestroy(t, newCobraTestRuntime(&cobraFakeProvider{}))
	if err != nil {
		t.Fatalf("expected clean exit, got: %v", err)
	}
	testutil.AssertContains(t, got, "To destroy infrastructure, use --all:")
}

// TestDestroyCobraDryRunNoAWSCalls verifies that --all --dry-run produces
// plan output and makes zero delete calls.
func TestDestroyCobraDryRunNoAWSCalls(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runDestroy(t, newCobraTestRuntime(provider), "--all", "--dry-run")
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	if provider.bucketDeleteCalls != 0 || provider.tableDeleteCalls != 0 {
		t.Fatalf("dry run made AWS calls: bucketDeleteCalls=%d tableDeleteCalls=%d",
			provider.bucketDeleteCalls, provider.tableDeleteCalls)
	}
	testutil.AssertContains(t, got, "Nothing has been deleted. Run without --dry-run to proceed.")
}

// TestDestroyCobraDryRunWithNilProvider verifies that --all --dry-run with
// a nil provider exits cleanly (hits the nil-provider guard, no panic).
func TestDestroyCobraDryRunWithNilProvider(t *testing.T) {
	got, err := runDestroy(t, newNilProviderRuntime(), "--all", "--dry-run")
	if err != nil {
		t.Fatalf("expected clean exit with nil provider in dry-run, got: %v", err)
	}
	testutil.AssertContains(t, got, "No infrastructure found. Nothing to destroy.")
}

// TestDestroyCobraDryRunOutput verifies dry-run output contains all key fields.
func TestDestroyCobraDryRunOutput(t *testing.T) {
	got, err := runDestroy(t, newCobraTestRuntime(&cobraFakeProvider{}), "--all", "--dry-run")
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	checks := []string{
		"Destroy --all dry run",
		"Account: 123456789012",
		"Region:  us-east-1",
		"S3 bucket:      fabrica-state-test",
		"DynamoDB table: fabrica-locks-test",
	}
	for _, want := range checks {
		testutil.AssertContains(t, got, want)
	}
}

// TestDestroyCobraYesFlagPerformsDeletion verifies that --all --yes
// deletes both resources and reports completion.
func TestDestroyCobraYesFlagPerformsDeletion(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runDestroy(t, newCobraTestRuntime(provider), "--all", "--yes")
	if err != nil {
		t.Fatalf("destroy failed: %v", err)
	}
	if !provider.deletedBucket {
		t.Fatal("S3 bucket was not deleted")
	}
	if !provider.deletedTable {
		t.Fatal("DynamoDB table was not deleted")
	}
	testutil.AssertContains(t, got, "Destroy --all complete. All modules and the state backend were removed.")
}

// TestDestroyCobraNilProvider verifies that --all --yes with a nil provider
// exits cleanly with an informational message and does not panic.
func TestDestroyCobraNilProvider(t *testing.T) {
	got, err := runDestroy(t, newNilProviderRuntime(), "--all", "--yes")
	if err != nil {
		t.Fatalf("expected clean exit with nil provider, got: %v", err)
	}
	testutil.AssertContains(t, got, "No infrastructure found. Nothing to destroy.")
}

// TestDestroyCobraIdentityFailurePropagates verifies that an identity
// resolution error surfaces as a command error.
func TestDestroyCobraIdentityFailurePropagates(t *testing.T) {
	provider := &cobraFakeProvider{identityErr: errors.New("credentials unavailable")}
	_, err := runDestroy(t, newCobraTestRuntime(provider), "--all", "--yes")
	if err == nil {
		t.Fatal("expected error when identity fails")
	}
	if !cobraContainsString(err.Error(), "resolving identity") {
		t.Fatalf("error %q does not mention resolving identity", err.Error())
	}
}

// TestDestroyCobraAllWithModules verifies destroy --all with modules provisioned.
// This drives the teardownClosure for perforce and ciTeardownClosure for CI.
func TestDestroyCobraAllWithModules(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Seed state with perforce and ci modules.
	stateWithModules := `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"perforce","version":"2024.2","status":"ready","resources":[
			{"typeName":"AWS::EC2::SecurityGroup","identifier":"sg-pf"},
			{"typeName":"AWS::EC2::Instance","identifier":"i-pf"}
		]},
		{"name":"ci","version":"fabrica-ci","status":"ready","resources":[
			{"typeName":"AWS::CodeBuild::Project","identifier":"fabrica-ci"},
			{"typeName":"AWS::IAM::Role","identifier":"fabrica-ci-role"}
		]}
	]}`

	testutil.WriteStateFile(t, dir, stateWithModules)

	provider := &cobraFakeProviderWithCI{}
	got, err := runDestroy(t, newCobraTestRuntime(provider), "--all", "--yes")
	if err != nil {
		t.Fatalf("destroy --all with modules: %v", err)
	}

	// Both module teardowns should have been called
	if provider.moduleDeleteCalls == 0 {
		t.Error("expected module teardown calls")
	}

	// Backend deletion should have occurred
	if !provider.deletedBucket || !provider.deletedTable {
		t.Fatal("backend should be deleted when all modules succeed")
	}

	testutil.AssertContains(t, got, "complete")
}

// TestDestroyCobraReadStateError verifies error when state read fails.
func TestDestroyCobraReadStateError(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.State.Table = "fabrica-locks-test"

	rt := globals.Runtime{
		Config:   cfg,
		Provider: &stateErrorProvider{},
	}

	src := func() (globals.Runtime, error) { return rt, nil }

	_, err := runDestroy(t, src, "--all", "--yes")
	if err == nil {
		t.Fatal("expected error when state read fails")
	}
}

// cobraFakeProviderWithCI extends cobraFakeProvider to handle module teardowns.
type cobraFakeProviderWithCI struct {
	cobraFakeProvider
	moduleDeleteCalls int
}

func (f *cobraFakeProviderWithCI) Resources() cloud.ResourceClient {
	return &cobraFakeRCWithDelete{provider: f}
}

// Implement cloud.CodeBuildRunner for CI module teardown.
func (f *cobraFakeProviderWithCI) EnsureProject(ctx context.Context, spec cloud.CodeBuildProjectSpec) (bool, error) {
	return true, nil
}

func (f *cobraFakeProviderWithCI) DeleteProject(ctx context.Context, name string) error {
	f.moduleDeleteCalls++
	return nil
}

func (f *cobraFakeProviderWithCI) StartBuild(ctx context.Context, project string, env map[string]string) (string, error) {
	return "build-1", nil
}

func (f *cobraFakeProviderWithCI) BuildStatus(ctx context.Context, buildID string) (cloud.BuildInfo, error) {
	return cloud.BuildInfo{}, nil
}

func (f *cobraFakeProviderWithCI) BuildLog(ctx context.Context, buildID string) (string, error) {
	return "", nil
}

type cobraFakeRCWithDelete struct {
	provider *cobraFakeProviderWithCI
}

func (r *cobraFakeRCWithDelete) Create(ctx context.Context, res *cloud.Resource) error { return nil }
func (r *cobraFakeRCWithDelete) Get(ctx context.Context, res *cloud.Resource) error    { return nil }
func (r *cobraFakeRCWithDelete) Update(ctx context.Context, res *cloud.Resource) error { return nil }
func (r *cobraFakeRCWithDelete) Delete(ctx context.Context, res *cloud.Resource) error {
	r.provider.moduleDeleteCalls++
	return nil
}
func (r *cobraFakeRCWithDelete) List(ctx context.Context, typeName string) ([]cloud.Resource, error) {
	return nil, nil
}

// stateErrorProvider simulates a provider that exists but fails on Identity.
type stateErrorProvider struct{}

func (stateErrorProvider) Name() string { return "err" }
func (stateErrorProvider) Identity(ctx context.Context) (string, string, string, error) {
	return "", "", "", errors.New("identity failed")
}
func (stateErrorProvider) Resources() cloud.ResourceClient { return nil }

// cobraFakeProvider is a minimal fake satisfying cloud.Provider and
// cloud.StateBackendDestroyer for Cobra-layer tests.
type cobraFakeProvider struct {
	deletedBucket     bool
	deletedTable      bool
	identityErr       error
	bucketDeleteCalls int
	tableDeleteCalls  int
}

func (f *cobraFakeProvider) Name() string { return "fake" }

func (f *cobraFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	if f.identityErr != nil {
		return "", "", "", f.identityErr
	}
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *cobraFakeProvider) Resources() cloud.ResourceClient { return nil }

func (f *cobraFakeProvider) DeleteStateBucket(_ context.Context, bucket string) (cloud.StateBackendDeleteResult, error) {
	f.bucketDeleteCalls++
	f.deletedBucket = true
	return cloud.StateBackendDeleteResult{Identifier: bucket, Deleted: true}, nil
}

func (f *cobraFakeProvider) DeleteStateLockTable(_ context.Context, table string) (cloud.StateBackendDeleteResult, error) {
	f.tableDeleteCalls++
	f.deletedTable = true
	return cloud.StateBackendDeleteResult{Identifier: table, Deleted: true}, nil
}

func cobraContainsString(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
