package trigger_test

import (
	"bytes"
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
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(trigger.New(runtimeSource, optionsSource, out))
	return root
}

func runCITrigger(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"trigger"}, args...)...)
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
	return testutil.NewProvisionedStateJSON(
		testutil.StateModule{
			Name: "ci", Version: "fabrica-ci", Status: "ready",
			Resources: []testutil.StateResource{
				{TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci"},
			},
		},
		testutil.StateModule{
			Name: "horde", Version: "ami-1", Status: "ready",
			Resources: []testutil.StateResource{
				{TypeName: "AWS::EC2::Instance", Identifier: "i-horde123"},
			},
		},
	)
}

func newTriggerProvider(startBuildID string) *testutil.CodeBuildProvider {
	return &testutil.CodeBuildProvider{
		TestProvider: testutil.TestProvider{
			GetResources: map[string]cloud.Resource{
				cloud.TypeAWSEC2Instance: {
					ActualState: []byte(`{"PrivateIpAddress":"10.0.1.42"}`),
				},
			},
		},
		StartBuildID: startBuildID,
	}
}

// TestTriggerCobraHappyPath starts a build via the real Cobra entry point.
func TestTriggerCobraHappyPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())
	bg := writeBuildGraph(t, dir)

	provider := newTriggerProvider("fabrica-ci:abc123")
	got, err := runCITrigger(t, testutil.NewTestRuntime(provider), bg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "Build started: fabrica-ci:abc123")
	testutil.AssertContains(t, got, "fabrica-ci")
	testutil.AssertContains(t, got, "Compile")
	testutil.AssertContains(t, got, "fabrica ci status")
	if provider.StartBuildCalls != 1 {
		t.Errorf("expected 1 StartBuild call, got %d", provider.StartBuildCalls)
	}
	if provider.LastStartBuildEnv["HORDE_URL"] != "http://10.0.1.42:5000" {
		t.Errorf("HORDE_URL = %q, want http://10.0.1.42:5000", provider.LastStartBuildEnv["HORDE_URL"])
	}
	if provider.LastStartBuildEnv["TARGET"] != "Compile" {
		t.Errorf("TARGET = %q, want Compile", provider.LastStartBuildEnv["TARGET"])
	}
}

// TestTriggerCobraNotProvisioned fails cleanly when CI state is missing.
func TestTriggerCobraNotProvisioned(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	bg := writeBuildGraph(t, dir)

	_, err := runCITrigger(t, testutil.NewTestRuntime(newTriggerProvider("x")), bg)
	if err == nil {
		t.Fatal("expected error when CI is not provisioned")
	}
	testutil.AssertContains(t, err.Error(), "ci setup")
}

// TestTriggerCobraMissingBuildGraphArg enforces ExactArgs via Cobra.
func TestTriggerCobraMissingBuildGraphArg(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := runCITrigger(t, testutil.NewTestRuntime(newTriggerProvider("")))
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
	provider := newTriggerProvider("x")
	_, err := runCITrigger(t, testutil.NewTestRuntime(provider), path)
	if err == nil {
		t.Fatal("expected parse error for invalid BuildGraph")
	}
	if provider.StartBuildCalls != 0 {
		t.Errorf("must not start build on parse failure, got %d calls", provider.StartBuildCalls)
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
