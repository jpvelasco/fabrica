package trigger_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ci/trigger"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, opts := testutil.BuildTestRoot(out)
	optionsSource := func() globals.Options { return *opts }
	root.AddCommand(trigger.New(runtimeSource, optionsSource, out))
	return root
}

func runCITrigger(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"trigger"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func writeBuildGraph(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "BuildGraph.xml")
	xml := `<?xml version="1.0"?><BuildGraph xmlns="http://www.epicgames.com/BuildGraph">
		<Agent Name="BuildAgent" Type="Win64"><Node Name="Compile"/></Agent>
	</BuildGraph>`
	if err := os.WriteFile(path, []byte(xml), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func provisionedStateJSON() string {
	return `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"ci","version":"fabrica-ci","status":"ready","resources":[
			{"typeName":"AWS::CodeBuild::Project","identifier":"fabrica-ci"}
		]},
		{"name":"horde","version":"ami-1","status":"ready","resources":[
			{"typeName":"AWS::EC2::Instance","identifier":"i-horde123"}
		]}]}`
}

// TestTriggerCobraHappyPath starts a build via the real Cobra entry point.
func TestTriggerCobraHappyPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())
	bg := writeBuildGraph(t, dir)

	provider := &ciTriggerFakeProvider{startID: "fabrica-ci:abc123"}
	got, err := runCITrigger(t, testutil.NewTestRuntime(provider), bg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "Build started: fabrica-ci:abc123")
	testutil.AssertContains(t, got, "fabrica-ci")
	testutil.AssertContains(t, got, "Compile")
	testutil.AssertContains(t, got, "fabrica ci status")
	if provider.startCalls != 1 {
		t.Errorf("expected 1 StartBuild call, got %d", provider.startCalls)
	}
	if provider.lastEnv["HORDE_URL"] != "http://10.0.1.42:5000" {
		t.Errorf("HORDE_URL = %q, want http://10.0.1.42:5000", provider.lastEnv["HORDE_URL"])
	}
	if provider.lastEnv["TARGET"] != "Compile" {
		t.Errorf("TARGET = %q, want Compile", provider.lastEnv["TARGET"])
	}
}

// TestTriggerCobraNotProvisioned fails cleanly when CI state is missing.
func TestTriggerCobraNotProvisioned(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	bg := writeBuildGraph(t, dir)

	_, err := runCITrigger(t, testutil.NewTestRuntime(&ciTriggerFakeProvider{startID: "x"}), bg)
	if err == nil {
		t.Fatal("expected error when CI is not provisioned")
	}
	testutil.AssertContains(t, err.Error(), "ci setup")
}

// TestTriggerCobraMissingBuildGraphArg enforces ExactArgs via Cobra.
func TestTriggerCobraMissingBuildGraphArg(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := runCITrigger(t, testutil.NewTestRuntime(&ciTriggerFakeProvider{}))
	if err == nil {
		t.Fatal("expected error when buildgraph path is omitted")
	}
}

// TestTriggerCobraBadBuildGraph fails fast before AWS calls.
func TestTriggerCobraBadBuildGraph(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	path := filepath.Join(dir, "bad.xml")
	if err := os.WriteFile(path, []byte("not xml"), 0600); err != nil {
		t.Fatal(err)
	}
	provider := &ciTriggerFakeProvider{startID: "x"}
	_, err := runCITrigger(t, testutil.NewTestRuntime(provider), path)
	if err == nil {
		t.Fatal("expected parse error for invalid BuildGraph")
	}
	if provider.startCalls != 0 {
		t.Errorf("must not start build on parse failure, got %d calls", provider.startCalls)
	}
}

// TestTriggerCobraRuntimeError surfaces runtimeSource failures.
func TestTriggerCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	_, err := runCITrigger(t, src, "BuildGraph.xml")
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// ---- fakes ----

type ciTriggerFakeProvider struct {
	startID    string
	startCalls int
	lastEnv    map[string]string
}

func (f *ciTriggerFakeProvider) Name() string { return "fake" }

func (f *ciTriggerFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *ciTriggerFakeProvider) Resources() cloud.ResourceClient {
	return &ciTriggerFakeRC{}
}

func (f *ciTriggerFakeProvider) EnsureProject(_ context.Context, _ cloud.CodeBuildProjectSpec) (bool, error) {
	return true, nil
}

func (f *ciTriggerFakeProvider) DeleteProject(_ context.Context, _ string) error { return nil }

func (f *ciTriggerFakeProvider) StartBuild(_ context.Context, _ string, env map[string]string) (string, error) {
	f.startCalls++
	f.lastEnv = env
	if f.startID == "" {
		return "build-1", nil
	}
	return f.startID, nil
}

func (f *ciTriggerFakeProvider) BuildStatus(_ context.Context, _ string) (cloud.BuildInfo, error) {
	return cloud.BuildInfo{Status: "SUCCEEDED", Phase: "COMPLETED"}, nil
}

func (f *ciTriggerFakeProvider) BuildLog(_ context.Context, _ string) (string, error) {
	return "", nil
}

type ciTriggerFakeRC struct{}

func (r *ciTriggerFakeRC) Create(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *ciTriggerFakeRC) Get(_ context.Context, res *cloud.Resource) error {
	res.ActualState = []byte(`{"PrivateIpAddress":"10.0.1.42"}`)
	return nil
}
func (r *ciTriggerFakeRC) Update(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *ciTriggerFakeRC) Delete(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *ciTriggerFakeRC) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}
