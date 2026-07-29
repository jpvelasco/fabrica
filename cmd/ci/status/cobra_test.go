package status_test

import (
	"bytes"
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
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(status.New(runtimeSource, optionsSource, out))
	return root
}

// runCIStatus builds the command tree, sets args, and executes.
func runCIStatus(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"status"}, args...)...)
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

// TestCIStatusCobraNotProvisioned verifies clean message when no CI state exists.
func TestCIStatusCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runCIStatus(t, testutil.NewTestRuntime(&testutil.CodeBuildProvider{}))
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

	got, err := runCIStatus(t, testutil.NewTestRuntime(&testutil.CodeBuildProvider{}))
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

	got, err := runCIStatus(t, testutil.NewTestRuntime(&testutil.CodeBuildProvider{}))
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

	provider := &testutil.CodeBuildProvider{
		BuildInfo: cloud.BuildInfo{
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

	provider := &testutil.CodeBuildProvider{
		BuildInfo: cloud.BuildInfo{
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
	got, err := runCIStatus(t, testutil.NewTestRuntime(&testutil.CodeBuildProvider{}), "--json")
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

	got, err := runCIStatus(t, testutil.NewTestRuntime(&testutil.CodeBuildProvider{}), "--json")
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

	provider := &testutil.CodeBuildProvider{
		BuildInfo: cloud.BuildInfo{
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
	var p cloud.Provider = &testutil.CodeBuildProvider{}
	if _, ok := p.(cloud.CodeBuildRunner); !ok {
		t.Fatal("CodeBuildProvider does not implement cloud.CodeBuildRunner")
	}
}
