package status_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/horde/agents/status"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(status.New(runtimeSource, optionsSource, out))
	return root
}

func runAgentsStatus(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"status"}, args...)...)
}

func newStatusTestRuntime() globals.RuntimeSource {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg}
	return func() (globals.Runtime, error) { return rt, nil }
}

func TestAgentsStatusNotProvisioned(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// Empty state — no horde module at all.
	testutil.WriteStateFile(t, dir, `{"account":"123456789012","region":"us-east-1","modules":[]}`)

	got, err := runAgentsStatus(t, newStatusTestRuntime())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
}

func TestAgentsStatusNoAgents(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// Horde module exists but has no ASG — only coordinator.
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

	got, err := runAgentsStatus(t, newStatusTestRuntime())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
}

func TestAgentsStatusProvisioned(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// Horde module with coordinator + agent resources.
	stateJSON := testutil.NewProvisionedStateJSON(
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
				{
					TypeName:   "AWS::AutoScaling::AutoScalingGroup",
					Identifier: "asg-agent123",
					Properties: map[string]any{
						"role":            "agent",
						"minSize":         "0",
						"desiredCapacity": "2",
						"maxSize":         "4",
						"instanceType":    "c7i.xlarge",
						"imageId":         "ami-agent123",
					},
				},
			},
		},
	)
	testutil.WriteStateFile(t, dir, stateJSON)

	got, err := runAgentsStatus(t, newStatusTestRuntime())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "Horde agent pool")
	testutil.AssertContains(t, got, "asg-agent123")
	testutil.AssertContains(t, got, "lt-agent123")
	testutil.AssertContains(t, got, "c7i.xlarge")
}

func TestAgentsStatusJSON(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	stateJSON := testutil.NewProvisionedStateJSON(
		testutil.StateModule{
			Name:    "horde",
			Version: "ami-test123",
			Status:  "ready",
			Resources: []testutil.StateResource{
				{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-coordinator"},
				{TypeName: "AWS::EC2::Instance", Identifier: "i-coordinator"},
				{TypeName: "AWS::EC2::LaunchTemplate", Identifier: "lt-agent123", Properties: map[string]any{"role": "agent"}},
				{
					TypeName:   "AWS::AutoScaling::AutoScalingGroup",
					Identifier: "asg-agent123",
					Properties: map[string]any{
						"role":            "agent",
						"minSize":         "0",
						"desiredCapacity": "1",
						"maxSize":         "2",
					},
				},
			},
		},
	)
	testutil.WriteStateFile(t, dir, stateJSON)

	got, err := runAgentsStatus(t, newStatusTestRuntime(), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, `"provisioned": true`)
	testutil.AssertContains(t, got, `"asgId": "asg-agent123"`)
}

func TestAgentsStatusNotProvisionedJSON(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, `{"account":"123456789012","region":"us-east-1","modules":[]}`)

	got, err := runAgentsStatus(t, newStatusTestRuntime(), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, `"provisioned": false`)
}

func TestAgentsStatusLiveASG(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	stateJSON := testutil.NewProvisionedStateJSON(
		testutil.StateModule{
			Name:    "horde",
			Version: "ami-test123",
			Status:  "ready",
			Resources: []testutil.StateResource{
				{TypeName: "AWS::EC2::Instance", Identifier: "i-coordinator"},
				{TypeName: "AWS::EC2::LaunchTemplate", Identifier: "lt-agent123", Properties: map[string]any{"role": "agent"}},
				{
					TypeName:   "AWS::AutoScaling::AutoScalingGroup",
					Identifier: "asg-agent123",
					Properties: map[string]any{
						"role":            "agent",
						"minSize":         "0",
						"desiredCapacity": "2",
						"maxSize":         "4",
						"instanceType":    "c7i.xlarge",
						"imageId":         "ami-agent123",
					},
				},
			},
		},
	)
	testutil.WriteStateFile(t, dir, stateJSON)

	// Provider implements ASGManager seam — returns live lifecycle data.
	provider := &testutil.TestProvider{
		ASGInfo: &cloud.ASGInfo{
			Name:            "asg-agent123",
			DesiredCapacity: 2,
			MinSize:         0,
			MaxSize:         4,
			InService:       2,
			Pending:         0,
			Terminating:     0,
		},
	}
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: provider}
	runtimeSource := func() (globals.Runtime, error) { return rt, nil }

	got, err := runAgentsStatus(t, runtimeSource)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should show live capacity from ASG SDK query.
	testutil.AssertContains(t, got, "Live Capacity")
	testutil.AssertContains(t, got, "0/2/4")
	testutil.AssertContains(t, got, "Health")
	testutil.AssertContains(t, got, "2/2 InService")
}

func TestAgentsStatusLiveASGJSON(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	stateJSON := testutil.NewProvisionedStateJSON(
		testutil.StateModule{
			Name:    "horde",
			Version: "ami-test123",
			Status:  "ready",
			Resources: []testutil.StateResource{
				{TypeName: "AWS::EC2::Instance", Identifier: "i-coordinator"},
				{
					TypeName:   "AWS::AutoScaling::AutoScalingGroup",
					Identifier: "asg-agent123",
					Properties: map[string]any{
						"role":            "agent",
						"minSize":         "0",
						"desiredCapacity": "1",
						"maxSize":         "2",
					},
				},
			},
		},
	)
	testutil.WriteStateFile(t, dir, stateJSON)

	provider := &testutil.TestProvider{
		ASGInfo: &cloud.ASGInfo{
			Name:            "asg-agent123",
			DesiredCapacity: 1,
			MinSize:         0,
			MaxSize:         2,
			InService:       1,
			Pending:         0,
			Terminating:     0,
		},
	}
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: provider}
	runtimeSource := func() (globals.Runtime, error) { return rt, nil }

	got, err := runAgentsStatus(t, runtimeSource, "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, `"liveDesiredCapacity": 1`)
	testutil.AssertContains(t, got, `"liveMinSize": 0`)
	testutil.AssertContains(t, got, `"liveMaxSize": 2`)
	testutil.AssertContains(t, got, `"inService": 1`)
	testutil.AssertContains(t, got, `"asgHealth"`)
}

func TestAgentsStatusScaledToZero(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	stateJSON := testutil.NewProvisionedStateJSON(
		testutil.StateModule{
			Name:    "horde",
			Version: "ami-test123",
			Status:  "ready",
			Resources: []testutil.StateResource{
				{TypeName: "AWS::EC2::Instance", Identifier: "i-coordinator"},
				{
					TypeName:   "AWS::AutoScaling::AutoScalingGroup",
					Identifier: "asg-agent123",
					Properties: map[string]any{
						"role":            "agent",
						"minSize":         "0",
						"desiredCapacity": "2",
						"maxSize":         "4",
					},
				},
			},
		},
	)
	testutil.WriteStateFile(t, dir, stateJSON)

	// ASG scaled to 0 — desired capacity is 0, no instances running.
	provider := &testutil.TestProvider{
		ASGInfo: &cloud.ASGInfo{
			Name:            "asg-agent123",
			DesiredCapacity: 0,
			MinSize:         0,
			MaxSize:         4,
			InService:       0,
			Pending:         0,
			Terminating:     0,
		},
	}
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: provider}
	runtimeSource := func() (globals.Runtime, error) { return rt, nil }

	got, err := runAgentsStatus(t, runtimeSource)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Live capacity should still be shown even when scaled to 0.
	testutil.AssertContains(t, got, "Live Capacity")
	testutil.AssertContains(t, got, "0/0/4")
	testutil.AssertContains(t, got, "scaled to 0")
}

func TestAgentsStatusZeroInService(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	stateJSON := testutil.NewProvisionedStateJSON(
		testutil.StateModule{
			Name:    "horde",
			Version: "ami-test123",
			Status:  "ready",
			Resources: []testutil.StateResource{
				{TypeName: "AWS::EC2::Instance", Identifier: "i-coordinator"},
				{
					TypeName:   "AWS::AutoScaling::AutoScalingGroup",
					Identifier: "asg-agent123",
					Properties: map[string]any{
						"role":            "agent",
						"minSize":         "0",
						"desiredCapacity": "2",
						"maxSize":         "4",
					},
				},
			},
		},
	)
	testutil.WriteStateFile(t, dir, stateJSON)

	// Desired 2, but 0 InService — instances are pending or terminating.
	provider := &testutil.TestProvider{
		ASGInfo: &cloud.ASGInfo{
			Name:            "asg-agent123",
			DesiredCapacity: 2,
			MinSize:         0,
			MaxSize:         4,
			InService:       0,
			Pending:         2,
			Terminating:     0,
		},
	}
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: provider}
	runtimeSource := func() (globals.Runtime, error) { return rt, nil }

	got, err := runAgentsStatus(t, runtimeSource)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should show the gap between desired and in-service.
	testutil.AssertContains(t, got, "Live Capacity")
	testutil.AssertContains(t, got, "0/2/4")
	testutil.AssertContains(t, got, "Pending")
	testutil.AssertContains(t, got, "Health")
	testutil.AssertContains(t, got, "0/2 InService")
}

// vpcTestProvider is not needed here — status doesn't resolve VPC.
var _ = context.Background
