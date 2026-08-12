package destroy_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/horde/agents/destroy"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return root
}

func runAgentsDestroy(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"destroy"}, args...)...)
}

func newDestroyTestRuntime() globals.RuntimeSource {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg}
	return func() (globals.Runtime, error) { return rt, nil }
}

// agentsStateJSON returns a state fixture with both coordinator and agent
// resources provisioned. Agent resources are marked with Properties["role"]
// = "agent" (matching the create command's behavior).
func agentsStateJSON() string {
	return testutil.NewProvisionedStateJSON(
		testutil.StateModule{
			Name:    "horde",
			Version: "ami-test123",
			Status:  "ready",
			Resources: []testutil.StateResource{
				{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-coordinator"},
				{TypeName: "AWS::EC2::Instance", Identifier: "i-coordinator"},
				{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-agent123", Properties: map[string]any{"role": "agent"}},
				{TypeName: "AWS::IAM::Role", Identifier: "role-agent123", Properties: map[string]any{"role": "agent"}},
				{TypeName: "AWS::IAM::InstanceProfile", Identifier: "profile-agent123", Properties: map[string]any{"role": "agent"}},
				{TypeName: "AWS::EC2::LaunchTemplate", Identifier: "lt-agent123", Properties: map[string]any{"role": "agent"}},
				{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent123", Properties: map[string]any{"role": "agent"}},
			},
		},
	)
}

func TestAgentsDestroyNotProvisioned(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// No horde module at all.
	testutil.WriteStateFile(t, dir, `{"account":"123456789012","region":"us-east-1","modules":[]}`)

	got, err := runAgentsDestroy(t, newDestroyTestRuntime(), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
}

func TestAgentsDestroyNoAgentsOnlyCoordinator(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// Horde module exists but has no agent resources.
	stateJSON := testutil.NewProvisionedStateJSON(
		testutil.StateModule{
			Name:    "horde",
			Version: "ami-test123",
			Status:  "ready",
			Resources: []testutil.StateResource{
				{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-coordinator"},
				{TypeName: "AWS::EC2::Instance", Identifier: "i-coordinator"},
			},
		},
	)
	testutil.WriteStateFile(t, dir, stateJSON)

	got, err := runAgentsDestroy(t, newDestroyTestRuntime(), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
}

func TestAgentsDestroyDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, agentsStateJSON())

	got, err := runAgentsDestroy(t, newDestroyTestRuntime(), "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	testutil.AssertContains(t, got, "destroy dry run")
	testutil.AssertContains(t, got, "asg-agent123")
	testutil.AssertContains(t, got, "lt-agent123")
	// Should NOT list coordinator resources
	testutil.AssertNotContains(t, got, "i-coordinator")
	testutil.AssertNotContains(t, got, "sg-coordinator")
}

func TestAgentsDestroyYesFlag(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, agentsStateJSON())

	provider := &testutil.TestProvider{}
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: provider}
	runtimeSource := func() (globals.Runtime, error) { return rt, nil }

	got, err := runAgentsDestroy(t, runtimeSource, "--yes")
	if err != nil {
		t.Fatalf("--yes destroy failed: %v", err)
	}
	// 5 agent resources: ASG, LT, profile, role, SG
	if provider.DeleteCalls != 5 {
		t.Fatalf("expected 5 delete calls, got %d", provider.DeleteCalls)
	}
	testutil.AssertContains(t, got, "destroyed")
}

func TestAgentsDestroyPreservesCoordinator(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, agentsStateJSON())

	provider := &testutil.TestProvider{}
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: provider}
	runtimeSource := func() (globals.Runtime, error) { return rt, nil }

	_, err := runAgentsDestroy(t, runtimeSource, "--yes")
	if err != nil {
		t.Fatalf("--yes destroy failed: %v", err)
	}

	// Verify coordinator resources are still in state.
	data, err := os.ReadFile(dir + "/.fabrica/state.json")
	if err != nil {
		t.Fatalf("reading state: %v", err)
	}
	// Coordinator instance and SG should still be present.
	testutil.AssertContains(t, string(data), "i-coordinator")
	testutil.AssertContains(t, string(data), "sg-coordinator")
	// Agent resources should be gone.
	testutil.AssertNotContains(t, string(data), "asg-agent123")
}
