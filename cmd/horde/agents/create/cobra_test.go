package create_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/horde/agents/create"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(create.New(runtimeSource, optionsSource, out))
	return root
}

func runAgentsCreate(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"create"}, args...)...)
}

// coordinatorStateJSON returns a state fixture with a provisioned horde
// coordinator (instance + SG) so that agents create can resolve the
// coordinator private IP.
func coordinatorStateJSON() string {
	return testutil.NewProvisionedStateJSON(
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
}

// newAgentsTestRuntime creates a runtime with an agent AMI and a provider
// that can resolve the coordinator's private IP via Get.
func newAgentsTestRuntime(provider cloud.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.State.Table = "fabrica-locks-test"
	cfg.Horde.Agents.AmiID = "ami-agent123"
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

func TestAgentsCreateDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Coordinator must exist in state.
	testutil.WriteStateFile(t, dir, coordinatorStateJSON())

	// Provider resolves coordinator IP via Get and provides VPC.
	provider := &testutil.TestProvider{
		GetResources: map[string]cloud.Resource{
			cloud.TypeAWSEC2Instance: {
				Identifier: "i-coordinator",
				ActualState: mustJSON(map[string]any{
					"PrivateIpAddress": "10.0.1.50",
				}),
			},
		},
	}
	// Wire VPC resolver so plan can resolve VPC/subnet.
	vpcProvider := &vpcTestProvider{TestProvider: provider, vpcID: "vpc-test", subnetID: "subnet-test"}

	got, err := runAgentsCreate(t, newAgentsTestRuntime(vpcProvider), "--dry-run", "--ami-id", "ami-agent123")
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	if provider.CreateCalls != 0 {
		t.Fatalf("dry-run made %d create calls, want 0", provider.CreateCalls)
	}
	testutil.AssertContains(t, got, "dry run")
	testutil.AssertContains(t, got, "ami-agent123")
	testutil.AssertContains(t, got, "fabrica-horde-agents-asg")
}

func TestAgentsCreateMissingAMIFlag(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, coordinatorStateJSON())

	// Provider can resolve coordinator IP, but no agent AMI is set.
	provider := &testutil.TestProvider{
		GetResources: map[string]cloud.Resource{
			cloud.TypeAWSEC2Instance: {
				Identifier: "i-coordinator",
				ActualState: mustJSON(map[string]any{
					"PrivateIpAddress": "10.0.1.50",
				}),
			},
		},
	}
	vpcProvider := &vpcTestProvider{TestProvider: provider, vpcID: "vpc-test", subnetID: "subnet-test"}

	// Config has no agent AMI set.
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.State.Table = "fabrica-locks-test"
	// Deliberately: no cfg.Horde.Agents.AmiID
	rt := globals.Runtime{Config: cfg, Provider: vpcProvider}
	runtimeSource := func() (globals.Runtime, error) { return rt, nil }

	_, err := runAgentsCreate(t, runtimeSource)
	if err == nil {
		t.Fatal("expected error when agent AMI is missing")
	}
	testutil.AssertContains(t, err.Error(), "horde.agents.amiId is required")
}

func TestAgentsCreateMissingCoordinator(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// No state at all — coordinator not provisioned.

	provider := &testutil.TestProvider{}
	_, err := runAgentsCreate(t, newAgentsTestRuntime(provider), "--ami-id", "ami-agent123")
	if err == nil {
		t.Fatal("expected error when coordinator is not provisioned")
	}
	testutil.AssertContains(t, err.Error(), "not provisioned")
}

func TestAgentsCreateNilProvider(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, coordinatorStateJSON())

	cfg := config.Defaults()
	cfg.Horde.Agents.AmiID = "ami-agent123"
	rt := globals.Runtime{Config: cfg, Provider: nil}
	runtimeSource := func() (globals.Runtime, error) { return rt, nil }

	_, err := runAgentsCreate(t, runtimeSource, "--ami-id", "ami-agent123")
	if err == nil {
		t.Fatal("expected error when provider is nil")
	}
	testutil.AssertContains(t, err.Error(), "no provider")
}

func TestAgentsCreateIdentityFailure(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, coordinatorStateJSON())

	provider := &testutil.TestProvider{IdentityErr: errors.New("credentials unavailable")}
	_, err := runAgentsCreate(t, newAgentsTestRuntime(provider), "--ami-id", "ami-agent123")
	if err == nil {
		t.Fatal("expected error when identity fails")
	}
	testutil.AssertContains(t, err.Error(), "could not resolve AWS identity")
}

func TestAgentsCreateAlreadyProvisioned(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// State has both coordinator and ASG — agents already provisioned.
	stateJSON := testutil.NewProvisionedStateJSON(
		testutil.StateModule{
			Name:    "horde",
			Version: "ami-test123",
			Status:  "ready",
			Resources: []testutil.StateResource{
				{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-coordinator"},
				{TypeName: "AWS::EC2::Instance", Identifier: "i-coordinator"},
				{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent123", Properties: map[string]any{"role": "agent"}},
			},
		},
	)
	testutil.WriteStateFile(t, dir, stateJSON)

	provider := &testutil.TestProvider{
		GetResources: map[string]cloud.Resource{
			cloud.TypeAWSEC2Instance: {
				Identifier: "i-coordinator",
				ActualState: mustJSON(map[string]any{
					"PrivateIpAddress": "10.0.1.50",
				}),
			},
		},
	}
	vpcProvider := &vpcTestProvider{TestProvider: provider, vpcID: "vpc-test", subnetID: "subnet-test"}

	got, err := runAgentsCreate(t, newAgentsTestRuntime(vpcProvider), "--ami-id", "ami-agent123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.CreateCalls != 0 {
		t.Fatalf("already provisioned: made %d create calls, want 0", provider.CreateCalls)
	}
	testutil.AssertContains(t, got, "already provisioned")
}

func TestAgentsCreateYesFlag(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, coordinatorStateJSON())

	provider := &testutil.TestProvider{
		GetResources: map[string]cloud.Resource{
			cloud.TypeAWSEC2Instance: {
				Identifier: "i-coordinator",
				ActualState: mustJSON(map[string]any{
					"PrivateIpAddress": "10.0.1.50",
				}),
			},
		},
	}
	vpcProvider := &vpcTestProvider{TestProvider: provider, vpcID: "vpc-test", subnetID: "subnet-test"}

	_, err := runAgentsCreate(t, newAgentsTestRuntime(vpcProvider), "--yes", "--ami-id", "ami-agent123")
	if err != nil {
		t.Fatalf("--yes run failed: %v", err)
	}
	// 5 resources: SG, role, profile, LT, ASG
	if provider.CreateCalls != 5 {
		t.Fatalf("--yes: expected 5 create calls, got %d", provider.CreateCalls)
	}
}

func TestAgentsCreatePreservesCoordinator(t *testing.T) {
	// Verify that creating agents does not wipe coordinator resources from state.
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, coordinatorStateJSON())

	provider := &testutil.TestProvider{
		GetResources: map[string]cloud.Resource{
			cloud.TypeAWSEC2Instance: {
				Identifier: "i-coordinator",
				ActualState: mustJSON(map[string]any{
					"PrivateIpAddress": "10.0.1.50",
				}),
			},
		},
	}
	vpcProvider := &vpcTestProvider{TestProvider: provider, vpcID: "vpc-test", subnetID: "subnet-test"}

	_, err := runAgentsCreate(t, newAgentsTestRuntime(vpcProvider), "--yes", "--ami-id", "ami-agent123")
	if err != nil {
		t.Fatalf("--yes run failed: %v", err)
	}

	// Read state and verify coordinator resources are still present.
	data, err := os.ReadFile(dir + "/.fabrica/state.json")
	if err != nil {
		t.Fatalf("reading state: %v", err)
	}
	var st fabricastate.State
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("parsing state: %v", err)
	}
	m := st.GetModule("horde")
	if m == nil {
		t.Fatal("horde module not found in state after agents create")
	}

	// Check coordinator instance exists.
	_, ok := stateutil.ResourceByType(m, cloud.TypeAWSEC2Instance)
	if !ok {
		t.Error("coordinator instance missing from state after agents create")
	}

	// Check coordinator SG exists (there should be 2 SGs: coordinator + agent).
	sgs := 0
	for _, r := range m.Resources {
		if r.TypeName == cloud.TypeAWSEC2SecurityGroup {
			sgs++
		}
	}
	if sgs < 2 {
		t.Errorf("expected at least 2 security groups (coordinator + agent), got %d", sgs)
	}

	// Check agent resources exist (ASG should be present).
	_, ok = stateutil.ResourceByType(m, cloud.TypeAWSAutoScalingAutoScalingGroup)
	if !ok {
		t.Error("ASG missing from state after agents create")
	}

	// Module Version must remain the coordinator AMI — not overwritten by agents.
	if m.Version != "ami-test123" {
		t.Errorf("module Version = %q, want ami-test123 (coordinator AMI preserved)", m.Version)
	}
}

func TestAgentsCreateFlags(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, coordinatorStateJSON())

	provider := &testutil.TestProvider{
		GetResources: map[string]cloud.Resource{
			cloud.TypeAWSEC2Instance: {
				Identifier: "i-coordinator",
				ActualState: mustJSON(map[string]any{
					"PrivateIpAddress": "10.0.1.50",
				}),
			},
		},
	}
	vpcProvider := &vpcTestProvider{TestProvider: provider, vpcID: "vpc-test", subnetID: "subnet-test"}

	got, err := runAgentsCreate(t, newAgentsTestRuntime(vpcProvider),
		"--dry-run", "--ami-id", "ami-agent123",
		"--instance-type", "c7i.2xlarge",
		"--min-size", "1", "--desired-capacity", "2", "--max-size", "4",
	)
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	testutil.AssertContains(t, got, "c7i.2xlarge")
	testutil.AssertContains(t, got, "1")
	testutil.AssertContains(t, got, "2")
	testutil.AssertContains(t, got, "4")
}

func TestAgentsCreateRuntimeError(t *testing.T) {
	runtimeSource := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not found")
	}
	_, err := runAgentsCreate(t, runtimeSource, "--dry-run")
	if err == nil {
		t.Fatal("expected error from runtimeSource")
	}
}

// mustJSON marshals v to JSON bytes, panicking on error (for test fixtures).
func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// vpcTestProvider wraps TestProvider with a VPCResolver so the plan can
// resolve VPC/subnet during dry-run and create.
type vpcTestProvider struct {
	*testutil.TestProvider
	vpcID    string
	subnetID string
}

func (v *vpcTestProvider) ResolveDefaultVPC(_ context.Context) (string, string, error) {
	return v.vpcID, v.subnetID, nil
}

// Ensure vpcTestProvider satisfies cloud.VPCResolver at compile time.
var _ cloud.VPCResolver = (*vpcTestProvider)(nil)
