package status_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ci/status"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/spf13/cobra"
)

// buildTestRoot constructs a minimal root command that mirrors the production
// flag hierarchy: --dry-run, --yes, and --json are persistent flags on root.
func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, opts := testutil.BuildTestRoot(out)
	optionsSource := func() globals.Options { return *opts }
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

// ciStateJSON returns a JSON string with CI module provisioned.
func ciStateJSON() string {
	return `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"ci","version":"fabrica-ci","status":"ready","resources":[
			{"typeName":"AWS::CodeBuild::Project","identifier":"fabrica-ci"},
			{"typeName":"AWS::IAM::Role","identifier":"fabrica-ci-codebuild"}
		]}]}`
}

// TestCIStatusCobraNotProvisioned verifies clean message when no CI state exists.
func TestCIStatusCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runCIStatus(t, testutil.NewTestRuntime(&ciCobraFakeProvider{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
}

// TestCIStatusCobraShowsInfrastructure verifies provisioned state renders infrastructure.
func TestCIStatusCobraShowsInfrastructure(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, ciStateJSON())

	got, err := runCIStatus(t, testutil.NewTestRuntime(&ciCobraFakeProvider{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "[OK]")
	testutil.AssertContains(t, got, "CodeBuild project")
	testutil.AssertContains(t, got, "IAM role")
	testutil.AssertContains(t, got, "fabrica-ci")
	testutil.AssertContains(t, got, "fabrica-ci-codebuild")
}

// TestCIStatusCobraShowsNextSteps verifies next steps guidance appears.
func TestCIStatusCobraShowsNextSteps(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, ciStateJSON())

	got, err := runCIStatus(t, testutil.NewTestRuntime(&ciCobraFakeProvider{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "Next steps:")
	testutil.AssertContains(t, got, "fabrica ci trigger")
}

// TestCIStatusCobraWithBuildID queries a live build and renders its status.
func TestCIStatusCobraWithBuildID(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, ciStateJSON())

	provider := &ciCobraFakeProvider{
		buildInfoResponse: cloud.BuildInfo{
			ID:     "fabrica-ci:1a2b3c4d",
			Status: "SUCCEEDED",
			Phase:  "COMPLETED",
		},
	}
	got, err := runCIStatus(t, testutil.NewTestRuntime(provider), "--build", "fabrica-ci:1a2b3c4d")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "SUCCEEDED")
	testutil.AssertContains(t, got, "COMPLETED")
}

// TestCIStatusCobraWithBuildIDInProgressStatus shows in-progress build.
func TestCIStatusCobraWithBuildIDInProgressStatus(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, ciStateJSON())

	provider := &ciCobraFakeProvider{
		buildInfoResponse: cloud.BuildInfo{
			ID:     "fabrica-ci:1a2b3c4d",
			Status: "IN_PROGRESS",
			Phase:  "BUILD",
		},
	}
	got, err := runCIStatus(t, testutil.NewTestRuntime(provider), "--build", "fabrica-ci:1a2b3c4d")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "IN_PROGRESS")
}

// TestCIStatusCobraJSONNotProvisioned verifies --json output when not provisioned.
func TestCIStatusCobraJSONNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runCIStatus(t, testutil.NewTestRuntime(&ciCobraFakeProvider{}), "--json")
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
	testutil.WriteStateFile(t, dir, ciStateJSON())

	got, err := runCIStatus(t, testutil.NewTestRuntime(&ciCobraFakeProvider{}), "--json")
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
	testutil.WriteStateFile(t, dir, ciStateJSON())

	provider := &ciCobraFakeProvider{
		buildInfoResponse: cloud.BuildInfo{
			ID:     "fabrica-ci:1a2b3c4d",
			Status: "SUCCEEDED",
			Phase:  "COMPLETED",
		},
	}
	got, err := runCIStatus(t, testutil.NewTestRuntime(provider), "--json", "--build", "fabrica-ci:1a2b3c4d")
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
	got, err := runCIStatus(t, testutil.NewNilProviderRuntime())
	if err != nil {
		t.Fatalf("nil provider: unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
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
