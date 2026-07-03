package destroy_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ci/destroy"
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
	root.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return root
}

// runCIDestroy builds the command tree, sets args, and executes.
func runCIDestroy(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"destroy"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

// newTestRuntime returns a RuntimeSource with a given provider.
func newTestRuntime(provider cloud.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

// newNilProviderRuntime returns a RuntimeSource with a nil provider.
func newNilProviderRuntime() globals.RuntimeSource {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: nil}
	return func() (globals.Runtime, error) { return rt, nil }
}

// ciStateJSON returns a JSON string with CI module provisioned.
func ciStateJSON() string {
	return `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"ci","version":"fabrica-ci","status":"ready","resources":[
			{"typeName":"AWS::CodeBuild::Project","identifier":"fabrica-ci"},
			{"typeName":"AWS::IAM::Role","identifier":"fabrica-ci-codebuild"}
		]}]}`
}

// writeStateFile writes CI state to the standard .fabrica/state.json location.
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

// TestCIDestroyCobraNotProvisioned verifies clean message when no CI state exists.
func TestCIDestroyCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runCIDestroy(t, newTestRuntime(&ciCobraFakeProvider{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "not provisioned")
}

// TestCIDestroyCobraDryRunNoDeleteCalls verifies --dry-run produces output without delete calls.
func TestCIDestroyCobraDryRunNoDeleteCalls(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, ciStateJSON())

	provider := &ciCobraFakeProvider{}
	got, err := runCIDestroy(t, newTestRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "dry run")
	if provider.projectDeleteCalls != 0 || provider.roleDeleteCalls != 0 {
		t.Errorf("dry-run made delete calls: project=%d role=%d", provider.projectDeleteCalls, provider.roleDeleteCalls)
	}
}

// TestCIDestroyCobraDryRunShowsResources verifies resource IDs appear in dry-run output.
func TestCIDestroyCobraDryRunShowsResources(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, ciStateJSON())

	got, err := runCIDestroy(t, newTestRuntime(&ciCobraFakeProvider{}), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "fabrica-ci")
	assertContains(t, got, "fabrica-ci-codebuild")
}

// TestCIDestroyCobraYesFlagDestroysResources verifies --yes destroys without prompt.
func TestCIDestroyCobraYesFlagDestroysResources(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, ciStateJSON())

	provider := &ciCobraFakeProvider{}
	got, err := runCIDestroy(t, newTestRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.projectDeleteCalls != 1 {
		t.Errorf("expected 1 project delete call, got %d", provider.projectDeleteCalls)
	}
	if provider.roleDeleteCalls != 1 {
		t.Errorf("expected 1 role delete call, got %d", provider.roleDeleteCalls)
	}
	assertContains(t, got, "destroyed")
}

// TestCIDestroyCobraJSONNotProvisioned verifies --json output when not provisioned.
func TestCIDestroyCobraJSONNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := runCIDestroy(t, newTestRuntime(&ciCobraFakeProvider{}), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCIDestroyCobraJSONDryRun verifies --json --dry-run work together.
func TestCIDestroyCobraJSONDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, ciStateJSON())

	_, err := runCIDestroy(t, newTestRuntime(&ciCobraFakeProvider{}), "--json", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCIDestroyCobraJSONYes verifies --json --yes work together.
func TestCIDestroyCobraJSONYes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, ciStateJSON())

	provider := &ciCobraFakeProvider{}
	_, err := runCIDestroy(t, newTestRuntime(provider), "--json", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.projectDeleteCalls != 1 || provider.roleDeleteCalls != 1 {
		t.Fatalf("expected both deletes, got project=%d role=%d", provider.projectDeleteCalls, provider.roleDeleteCalls)
	}
}

// TestCIDestroyCobraNilProvider verifies nil provider with no state exits cleanly.
func TestCIDestroyCobraNilProvider(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runCIDestroy(t, newNilProviderRuntime())
	if err != nil {
		t.Fatalf("nil provider: unexpected error: %v", err)
	}
	assertContains(t, got, "not provisioned")
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
	var p cloud.Provider = &ciCobraFakeProvider{}
	if _, ok := p.(cloud.CodeBuildRunner); !ok {
		t.Fatal("ciCobraFakeProvider does not implement cloud.CodeBuildRunner")
	}
}

// ciCobraFakeProvider is a minimal fake satisfying cloud.Provider and
// cloud.CodeBuildRunner for CI destroy Cobra-layer tests.
type ciCobraFakeProvider struct {
	projectDeleteCalls int
	roleDeleteCalls    int
}

func (f *ciCobraFakeProvider) Name() string { return "fake" }

func (f *ciCobraFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *ciCobraFakeProvider) Resources() cloud.ResourceClient {
	return &ciCobraFakeRC{provider: f}
}

// EnsureProject implements cloud.CodeBuildRunner.
func (f *ciCobraFakeProvider) EnsureProject(_ context.Context, _ cloud.CodeBuildProjectSpec) (bool, error) {
	return true, nil
}

// DeleteProject implements cloud.CodeBuildRunner.
func (f *ciCobraFakeProvider) DeleteProject(_ context.Context, name string) error {
	f.projectDeleteCalls++
	return nil
}

// StartBuild implements cloud.CodeBuildRunner.
func (f *ciCobraFakeProvider) StartBuild(_ context.Context, _ string, _ map[string]string) (string, error) {
	return "build-123", nil
}

// BuildStatus implements cloud.CodeBuildRunner.
func (f *ciCobraFakeProvider) BuildStatus(_ context.Context, _ string) (cloud.BuildInfo, error) {
	return cloud.BuildInfo{}, nil
}

// BuildLog implements cloud.CodeBuildRunner.
func (f *ciCobraFakeProvider) BuildLog(_ context.Context, _ string) (string, error) {
	return "", nil
}

type ciCobraFakeRC struct {
	provider *ciCobraFakeProvider
}

func (r *ciCobraFakeRC) Create(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *ciCobraFakeRC) Get(_ context.Context, _ *cloud.Resource) error    { return nil }
func (r *ciCobraFakeRC) Update(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *ciCobraFakeRC) Delete(_ context.Context, res *cloud.Resource) error {
	r.provider.roleDeleteCalls++
	return nil
}
func (r *ciCobraFakeRC) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}
