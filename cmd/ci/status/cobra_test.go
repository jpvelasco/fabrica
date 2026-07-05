package status_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ci/status"
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
	root.AddCommand(status.New(runtimeSource, optionsSource, out))
	return root
}

// runCIStatus builds the command tree, sets args, and executes.
func runCIStatus(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"status"}, args...))
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

// TestCIStatusCobraNotProvisioned verifies clean message when no CI state exists.
func TestCIStatusCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runCIStatus(t, newTestRuntime(&ciCobraFakeProvider{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "not provisioned")
}

// TestCIStatusCobraShowsInfrastructure verifies provisioned state renders infrastructure.
func TestCIStatusCobraShowsInfrastructure(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, ciStateJSON())

	got, err := runCIStatus(t, newTestRuntime(&ciCobraFakeProvider{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "[OK]")
	assertContains(t, got, "CodeBuild project")
	assertContains(t, got, "IAM role")
	assertContains(t, got, "fabrica-ci")
	assertContains(t, got, "fabrica-ci-codebuild")
}

// TestCIStatusCobraShowsNextSteps verifies next steps guidance appears.
func TestCIStatusCobraShowsNextSteps(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, ciStateJSON())

	got, err := runCIStatus(t, newTestRuntime(&ciCobraFakeProvider{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "Next steps:")
	assertContains(t, got, "fabrica ci trigger")
}

// TestCIStatusCobraWithBuildID queries a live build and renders its status.
func TestCIStatusCobraWithBuildID(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, ciStateJSON())

	provider := &ciCobraFakeProvider{
		buildInfoResponse: cloud.BuildInfo{
			ID:     "fabrica-ci:1a2b3c4d",
			Status: "SUCCEEDED",
			Phase:  "COMPLETED",
		},
	}
	got, err := runCIStatus(t, newTestRuntime(provider), "--build", "fabrica-ci:1a2b3c4d")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "SUCCEEDED")
	assertContains(t, got, "COMPLETED")
}

// TestCIStatusCobraWithBuildIDInProgressStatus shows in-progress build.
func TestCIStatusCobraWithBuildIDInProgressStatus(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, ciStateJSON())

	provider := &ciCobraFakeProvider{
		buildInfoResponse: cloud.BuildInfo{
			ID:     "fabrica-ci:1a2b3c4d",
			Status: "IN_PROGRESS",
			Phase:  "BUILD",
		},
	}
	got, err := runCIStatus(t, newTestRuntime(provider), "--build", "fabrica-ci:1a2b3c4d")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "IN_PROGRESS")
}

// TestCIStatusCobraJSONNotProvisioned verifies --json output when not provisioned.
func TestCIStatusCobraJSONNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runCIStatus(t, newTestRuntime(&ciCobraFakeProvider{}), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var o status.StatusOutput
	if err := json.Unmarshal([]byte(got), &o); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, got)
	}
	if o.Provisioned {
		t.Errorf("expected Provisioned=false, got %+v", o)
	}
}

// TestCIStatusCobraJSONProvisioned verifies --json decodes StatusOutput correctly.
func TestCIStatusCobraJSONProvisioned(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, ciStateJSON())

	got, err := runCIStatus(t, newTestRuntime(&ciCobraFakeProvider{}), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var o status.StatusOutput
	if err := json.Unmarshal([]byte(got), &o); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, got)
	}
	if !o.Provisioned {
		t.Errorf("expected Provisioned=true, got %+v", o)
	}
	if o.Project != "fabrica-ci" {
		t.Errorf("expected Project='fabrica-ci', got %+v", o)
	}
	if o.Role != "fabrica-ci-codebuild" {
		t.Errorf("expected Role='fabrica-ci-codebuild', got %+v", o)
	}
}

// TestCIStatusCobraJSONWithBuildID includes build info in JSON output.
func TestCIStatusCobraJSONWithBuildID(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, ciStateJSON())

	provider := &ciCobraFakeProvider{
		buildInfoResponse: cloud.BuildInfo{
			ID:     "fabrica-ci:1a2b3c4d",
			Status: "SUCCEEDED",
			Phase:  "COMPLETED",
		},
	}
	got, err := runCIStatus(t, newTestRuntime(provider), "--json", "--build", "fabrica-ci:1a2b3c4d")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var o status.StatusOutput
	if err := json.Unmarshal([]byte(got), &o); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, got)
	}
	if o.BuildStatus != "SUCCEEDED" {
		t.Errorf("expected BuildStatus='SUCCEEDED', got %+v", o)
	}
	if o.BuildPhase != "COMPLETED" {
		t.Errorf("expected BuildPhase='COMPLETED', got %+v", o)
	}
}

// TestCIStatusCobraNilProvider verifies nil provider exits cleanly.
func TestCIStatusCobraNilProvider(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runCIStatus(t, newNilProviderRuntime())
	if err != nil {
		t.Fatalf("nil provider: unexpected error: %v", err)
	}
	assertContains(t, got, "not provisioned")
}

// TestCIStatusCobraRuntimeError verifies runtimeSource errors surface as command errors.
func TestCIStatusCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	_, err := runCIStatus(t, src)
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// TestCIStatusCobraFakeProviderImplementsInterfaces verifies the fake provider satisfies all required interfaces.
func TestCIStatusCobraFakeProviderImplementsInterfaces(t *testing.T) {
	var p cloud.Provider = &ciCobraFakeProvider{}
	if _, ok := p.(cloud.CodeBuildRunner); !ok {
		t.Fatal("ciCobraFakeProvider does not implement cloud.CodeBuildRunner")
	}
}

// ciCobraFakeProvider is a minimal fake satisfying cloud.Provider and
// cloud.CodeBuildRunner for CI status Cobra-layer tests.
type ciCobraFakeProvider struct {
	buildInfoResponse cloud.BuildInfo
	buildStatusErr    error
}

func (f *ciCobraFakeProvider) Name() string { return "fake" }

func (f *ciCobraFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *ciCobraFakeProvider) Resources() cloud.ResourceClient {
	return &ciCobraFakeRC{}
}

// EnsureProject implements cloud.CodeBuildRunner.
func (f *ciCobraFakeProvider) EnsureProject(_ context.Context, _ cloud.CodeBuildProjectSpec) (bool, error) {
	return true, nil
}

// DeleteProject implements cloud.CodeBuildRunner.
func (f *ciCobraFakeProvider) DeleteProject(_ context.Context, _ string) error {
	return nil
}

// StartBuild implements cloud.CodeBuildRunner.
func (f *ciCobraFakeProvider) StartBuild(_ context.Context, _ string, _ map[string]string) (string, error) {
	return "build-123", nil
}

// BuildStatus implements cloud.CodeBuildRunner.
func (f *ciCobraFakeProvider) BuildStatus(_ context.Context, _ string) (cloud.BuildInfo, error) {
	return f.buildInfoResponse, f.buildStatusErr
}

// BuildLog implements cloud.CodeBuildRunner.
func (f *ciCobraFakeProvider) BuildLog(_ context.Context, _ string) (string, error) {
	return "", nil
}

type ciCobraFakeRC struct{}

func (r *ciCobraFakeRC) Create(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *ciCobraFakeRC) Get(_ context.Context, _ *cloud.Resource) error    { return nil }
func (r *ciCobraFakeRC) Update(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *ciCobraFakeRC) Delete(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *ciCobraFakeRC) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}
