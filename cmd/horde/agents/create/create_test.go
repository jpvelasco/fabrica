package create

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/horde"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestAgentsProvisioned_True(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
		{TypeName: "AWS::AutoScaling::AutoScalingGroup", Identifier: "asg-agent", Properties: map[string]string{"role": "agent"}},
	})
	if !agentsProvisioned(st) {
		t.Error("expected agentsProvisioned = true")
	}
}

func TestAgentsProvisioned_False_NoModule(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	if agentsProvisioned(st) {
		t.Error("expected agentsProvisioned = false when no module")
	}
}

func TestAgentsProvisioned_False_NoASG(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	if agentsProvisioned(st) {
		t.Error("expected agentsProvisioned = false when no ASG")
	}
}

func TestResolveCoordinator_MissingModule(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := command{
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	_, _, _, _, err := c.resolveCoordinator(context.Background())
	if err == nil {
		t.Fatal("expected error when horde module not found")
	}
}

func TestResolveCoordinator_MissingInstance(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-coord"},
	})
	c := command{
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	_, _, _, _, err := c.resolveCoordinator(context.Background())
	if err == nil {
		t.Fatal("expected error when coordinator instance not found")
	}
}

func TestResolveCoordinator_NoProvider(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	c := command{
		readState:   func() (*fabricastate.State, error) { return st, nil },
		getResource: nil,
	}
	_, _, _, _, err := c.resolveCoordinator(context.Background())
	if err == nil {
		t.Fatal("expected error when no provider")
	}
}

func TestResolveCoordinator_DefaultPort(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	cfg := config.Defaults()
	c := command{
		runtime:   globals.Runtime{Config: cfg},
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(ctx context.Context, r *cloud.Resource) error {
			r.ActualState = json.RawMessage(`{"PrivateIpAddress":"10.0.1.50"}`)
			return nil
		},
	}
	_, port, _, _, err := c.resolveCoordinator(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Config.Horde.Port defaults to 0 → should fall back to 5000.
	if port != 5000 {
		t.Errorf("port = %d, want 5000 (default)", port)
	}
}

func TestResolveCoordinator_CustomPort(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	cfg := config.Defaults()
	cfg.Horde.Port = 8080
	c := command{
		runtime:   globals.Runtime{Config: cfg},
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(ctx context.Context, r *cloud.Resource) error {
			r.ActualState = json.RawMessage(`{"PrivateIpAddress":"10.0.1.50"}`)
			return nil
		},
	}
	_, port, _, _, err := c.resolveCoordinator(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if port != 8080 {
		t.Errorf("port = %d, want 8080 (custom)", port)
	}
}

func TestResolveCoordinator_GetResourceError(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	c := command{
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(ctx context.Context, r *cloud.Resource) error {
			return fmt.Errorf("service unavailable")
		},
	}
	_, _, _, _, err := c.resolveCoordinator(context.Background())
	if err == nil {
		t.Fatal("expected error when getResource fails")
	}
}

func TestResolveCoordinator_NoActualState(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	c := command{
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(ctx context.Context, r *cloud.Resource) error {
			r.ActualState = json.RawMessage(`{}`)
			return nil
		},
	}
	_, _, _, _, err := c.resolveCoordinator(context.Background())
	if err == nil {
		t.Fatal("expected error when ActualState is empty")
	}
}

func TestResolveCoordinator_EmptyPrivateIP(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	c := command{
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(ctx context.Context, r *cloud.Resource) error {
			r.ActualState = json.RawMessage(`{"PrivateIpAddress":""}`)
			return nil
		},
	}
	_, _, _, _, err := c.resolveCoordinator(context.Background())
	if err == nil {
		t.Fatal("expected error when PrivateIpAddress is empty")
	}
}

func TestResolveCoordinator_ReadStateError(t *testing.T) {
	c := command{
		readState: func() (*fabricastate.State, error) { return nil, fmt.Errorf("state error") },
	}
	_, _, _, _, err := c.resolveCoordinator(context.Background())
	if err == nil {
		t.Fatal("expected error when readState fails")
	}
}

func TestResolveCoordinator_NoSG(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(ctx context.Context, r *cloud.Resource) error {
			r.ActualState = json.RawMessage(`{"PrivateIpAddress":"10.0.1.50"}`)
			return nil
		},
	}
	ip, port, sgID, _, err := c.resolveCoordinator(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip != "10.0.1.50" {
		t.Errorf("ip = %q, want 10.0.1.50", ip)
	}
	if port != 5000 {
		t.Errorf("port = %d, want 5000", port)
	}
	if sgID != "" {
		t.Errorf("sgID = %q, want empty when no SG in state", sgID)
	}
}

func TestResolveCoordinator_WithSG(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-coord-123"},
	})
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(ctx context.Context, r *cloud.Resource) error {
			r.ActualState = json.RawMessage(`{"PrivateIpAddress":"10.0.1.50"}`)
			return nil
		},
	}
	_, _, sgID, _, err := c.resolveCoordinator(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sgID != "sg-coord-123" {
		t.Errorf("sgID = %q, want sg-coord-123", sgID)
	}
}

func TestResolveCoordinator_Placement(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-coord-123"},
	})
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(ctx context.Context, r *cloud.Resource) error {
			switch r.TypeName {
			case cloud.TypeAWSEC2Instance:
				r.ActualState = json.RawMessage(`{"PrivateIpAddress":"10.0.1.50","SubnetId":"subnet-abc123"}`)
			case cloud.TypeAWSEC2SecurityGroup:
				r.ActualState = json.RawMessage(`{"VpcId":"vpc-abc123"}`)
			}
			return nil
		},
	}
	_, _, _, placement, err := c.resolveCoordinator(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if placement.SubnetID != "subnet-abc123" {
		t.Errorf("placement.SubnetID = %q, want subnet-abc123 (from the instance)", placement.SubnetID)
	}
	if placement.VPCID != "vpc-abc123" {
		t.Errorf("placement.VPCID = %q, want vpc-abc123 (from the coordinator SG)", placement.VPCID)
	}
}

func TestResolveCoordinator_SGGetError(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-coord-123"},
	})
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(ctx context.Context, r *cloud.Resource) error {
			if r.TypeName == cloud.TypeAWSEC2SecurityGroup {
				return fmt.Errorf("vpc call failed")
			}
			r.ActualState = json.RawMessage(`{"PrivateIpAddress":"10.0.1.50","SubnetId":"subnet-abc123"}`)
			return nil
		},
	}
	_, _, _, _, err := c.resolveCoordinator(context.Background())
	if err == nil {
		t.Fatal("expected error when the coordinator SG lookup fails")
	}
	if !strings.Contains(err.Error(), "querying coordinator security group") {
		t.Errorf("error = %q, want 'querying coordinator security group'", err.Error())
	}
}

func TestResolveCoordinator_SGStateUnparseable(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-coord-123"},
	})
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(ctx context.Context, r *cloud.Resource) error {
			if r.TypeName == cloud.TypeAWSEC2SecurityGroup {
				r.ActualState = json.RawMessage(`"not-a-map"`)
				return nil
			}
			r.ActualState = json.RawMessage(`{"PrivateIpAddress":"10.0.1.50","SubnetId":"subnet-abc123"}`)
			return nil
		},
	}
	_, _, _, placement, err := c.resolveCoordinator(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// An unparseable SG state leaves the VPC unknown (best-effort): the
	// placement stays half-known and the plan falls back to the default VPC.
	if placement.VPCID != "" {
		t.Errorf("placement.VPCID = %q, want empty (best-effort on unparseable SG state)", placement.VPCID)
	}
	if placement.SubnetID != "subnet-abc123" {
		t.Errorf("placement.SubnetID = %q, want subnet-abc123", placement.SubnetID)
	}
}

func TestApplyCreate_SGError(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	plan := &horde.AgentsCreatePlan{
		ASGName:              "asg-test",
		LaunchTemplateName:   "lt-test",
		InstanceProfileName:  "profile-test",
		RoleName:             "role-test",
		SGName:               "sg-test",
		CoordinatorPrivateIP: "10.0.1.50",
		CoordinatorPort:      5000,
		AmiID:                "ami-123",
		InstanceType:         "c7i.xlarge",
		MinSize:              0,
		DesiredCapacity:      1,
		MaxSize:              2,
	}
	c := command{
		out: &out,
		createResource: func(ctx context.Context, r *cloud.Resource) error {
			return fmt.Errorf("create failed")
		},
		writeState: func(st *fabricastate.State) error { return nil },
	}
	err := c.applyCreate(context.Background(), st, plan)
	if err == nil {
		t.Fatal("expected error when SG creation fails")
	}
	if !strings.Contains(err.Error(), "creating agent security group") {
		t.Errorf("error = %q, want 'creating agent security group'", err.Error())
	}
}

func TestApplyCreate_RoleError(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	plan := &horde.AgentsCreatePlan{
		ASGName:              "asg-test",
		LaunchTemplateName:   "lt-test",
		InstanceProfileName:  "profile-test",
		RoleName:             "role-test",
		SGName:               "sg-test",
		CoordinatorPrivateIP: "10.0.1.50",
		CoordinatorPort:      5000,
		AmiID:                "ami-123",
		InstanceType:         "c7i.xlarge",
		MinSize:              0,
		DesiredCapacity:      1,
		MaxSize:              2,
	}
	callCount := 0
	c := command{
		out: &out,
		createResource: func(ctx context.Context, r *cloud.Resource) error {
			callCount++
			if callCount == 1 {
				r.Identifier = "sg-created"
				return nil
			}
			return fmt.Errorf("role create failed")
		},
		writeState: func(st *fabricastate.State) error { return nil },
	}
	err := c.applyCreate(context.Background(), st, plan)
	if err == nil {
		t.Fatal("expected error when role creation fails")
	}
	if !strings.Contains(err.Error(), "creating agent IAM role") {
		t.Errorf("error = %q, want 'creating agent IAM role'", err.Error())
	}
}

func TestApplyCreate_UserDataError(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	plan := &horde.AgentsCreatePlan{
		ASGName:              "asg-test",
		LaunchTemplateName:   "lt-test",
		InstanceProfileName:  "profile-test",
		RoleName:             "role-test",
		SGName:               "sg-test",
		CoordinatorPrivateIP: "",
		CoordinatorPort:      -1,
		AmiID:                "ami-123",
		InstanceType:         "c7i.xlarge",
		MinSize:              0,
		DesiredCapacity:      1,
		MaxSize:              2,
	}
	callCount := 0
	c := command{
		out: &out,
		createResource: func(ctx context.Context, r *cloud.Resource) error {
			callCount++
			r.Identifier = fmt.Sprintf("resource-%d", callCount)
			return nil
		},
		writeState: func(st *fabricastate.State) error { return nil },
	}
	err := c.applyCreate(context.Background(), st, plan)
	if err == nil {
		t.Fatal("expected error when user data generation fails")
	}
	if !strings.Contains(err.Error(), "generating agent user data") {
		t.Errorf("error = %q, want 'generating agent user data'", err.Error())
	}
}

func TestApplyCreate_WithScaling(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	plan := &horde.AgentsCreatePlan{
		ASGName:              "asg-test",
		LaunchTemplateName:   "lt-test",
		InstanceProfileName:  "profile-test",
		RoleName:             "role-test",
		SGName:               "sg-test",
		CoordinatorPrivateIP: "10.0.1.50",
		CoordinatorPort:      5000,
		AmiID:                "ami-123",
		InstanceType:         "c7i.xlarge",
		MinSize:              0,
		DesiredCapacity:      1,
		MaxSize:              2,
		// Scaling resources
		ScalingEnabled:     true,
		ScaleOutThreshold:  10.0,
		ScaleInThreshold:   2.0,
		ScaleInCooldown:    120,
		MetricName:         "ASGQueueDepth",
		MetricNamespace:    "Fabrica/HordeAgents",
		ScaleOutAlarmName:  "alarm-out",
		ScaleInAlarmName:   "alarm-in",
		ScaleOutPolicyName: "policy-out",
		ScaleInPolicyName:  "policy-in",
	}
	callCount := 0
	c := command{
		out: &out,
		createResource: func(ctx context.Context, r *cloud.Resource) error {
			callCount++
			r.Identifier = fmt.Sprintf("resource-%d", callCount)
			return nil
		},
		writeState: func(st *fabricastate.State) error { return nil },
	}
	err := c.applyCreate(context.Background(), st, plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 5 base resources + 4 scaling resources (2 policies + 2 alarms) = 9 total
	if callCount != 9 {
		t.Errorf("want 9 create calls (5 base + 4 scaling), got %d", callCount)
	}
}

func newScalingPlan() *horde.AgentsCreatePlan {
	return &horde.AgentsCreatePlan{
		ASGName:              "asg-test",
		LaunchTemplateName:   "lt-test",
		InstanceProfileName:  "profile-test",
		RoleName:             "role-test",
		SGName:               "sg-test",
		CoordinatorPrivateIP: "10.0.1.50",
		CoordinatorPort:      5000,
		AmiID:                "ami-123",
		InstanceType:         "c7i.xlarge",
		MinSize:              0,
		DesiredCapacity:      1,
		MaxSize:              2,
		ScalingEnabled:       true,
		ScaleOutThreshold:    10.0,
		ScaleInThreshold:     2.0,
		ScaleInCooldown:      120,
		MetricName:           "ASGQueueDepth",
		MetricNamespace:      "Fabrica/HordeAgents",
		ScaleOutAlarmName:    "alarm-out",
		ScaleInAlarmName:     "alarm-in",
		ScaleOutPolicyName:   "policy-out",
		ScaleInPolicyName:    "policy-in",
	}
}

func newScalingState() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	return st
}

func TestApplyCreate_ScalingErrors(t *testing.T) {
	tests := []struct {
		name          string
		failOnCall    int
		wantErrSubstr string
	}{
		{"scale-out-policy", 6, "scale-out policy"},
		{"scale-in-policy", 7, "scale-in policy"},
		{"scale-out-alarm", 8, "scale-out alarm"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			plan := newScalingPlan()
			callCount := 0
			c := command{
				out: &out,
				createResource: func(ctx context.Context, r *cloud.Resource) error {
					callCount++
					if callCount == tc.failOnCall {
						return fmt.Errorf("create failed")
					}
					r.Identifier = fmt.Sprintf("resource-%d", callCount)
					return nil
				},
				writeState: func(st *fabricastate.State) error { return nil },
			}
			err := c.applyCreate(context.Background(), newScalingState(), plan)
			if err == nil {
				t.Fatalf("expected error when %s creation fails", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantErrSubstr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tc.wantErrSubstr)
			}
		})
	}
}

func TestApplyCreate_WithoutScaling(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	plan := &horde.AgentsCreatePlan{
		ASGName:              "asg-test",
		LaunchTemplateName:   "lt-test",
		InstanceProfileName:  "profile-test",
		RoleName:             "role-test",
		SGName:               "sg-test",
		CoordinatorPrivateIP: "10.0.1.50",
		CoordinatorPort:      5000,
		AmiID:                "ami-123",
		InstanceType:         "c7i.xlarge",
		MinSize:              0,
		DesiredCapacity:      1,
		MaxSize:              2,
		ScalingEnabled:       false,
	}
	callCount := 0
	c := command{
		out: &out,
		createResource: func(ctx context.Context, r *cloud.Resource) error {
			callCount++
			r.Identifier = fmt.Sprintf("resource-%d", callCount)
			return nil
		},
		writeState: func(st *fabricastate.State) error { return nil },
	}
	err := c.applyCreate(context.Background(), st, plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only 5 base resources when scaling is disabled
	if callCount != 5 {
		t.Errorf("want 5 create calls (base only), got %d", callCount)
	}
}

func TestApplyCreate_GRPCIngressCreated(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	plan := &horde.AgentsCreatePlan{
		ASGName:              "asg-test",
		LaunchTemplateName:   "lt-test",
		InstanceProfileName:  "profile-test",
		RoleName:             "role-test",
		SGName:               "sg-test",
		CoordinatorPrivateIP: "10.0.1.50",
		CoordinatorPort:      5000,
		CoordinatorGRPCPort:  5002,
		CoordinatorSGID:      "sg-coord",
		AmiID:                "ami-123",
		InstanceType:         "c7i.xlarge",
		MinSize:              0,
		DesiredCapacity:      1,
		MaxSize:              2,
	}
	var typeNames []string
	c := command{
		out: &out,
		createResource: func(ctx context.Context, r *cloud.Resource) error {
			typeNames = append(typeNames, r.TypeName)
			r.Identifier = fmt.Sprintf("resource-%d", len(typeNames))
			return nil
		},
		writeState: func(st *fabricastate.State) error { return nil },
	}
	err := c.applyCreate(context.Background(), st, plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 5 base + HTTP ingress + gRPC ingress = 7
	if len(typeNames) != 7 {
		t.Fatalf("want 7 create calls (5 base + 2 ingress), got %d: %v", len(typeNames), typeNames)
	}
	if typeNames[1] != cloud.TypeAWSEC2SecurityGroupIngress || typeNames[2] != cloud.TypeAWSEC2SecurityGroupIngress {
		t.Errorf("calls 2-3 = %v, want both %s", typeNames[1:3], cloud.TypeAWSEC2SecurityGroupIngress)
	}
}

func TestApplyCreate_GRPCIngressSkippedWhenSamePort(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "v1", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-coord"},
	})
	plan := &horde.AgentsCreatePlan{
		ASGName:              "asg-test",
		LaunchTemplateName:   "lt-test",
		InstanceProfileName:  "profile-test",
		RoleName:             "role-test",
		SGName:               "sg-test",
		CoordinatorPrivateIP: "10.0.1.50",
		CoordinatorPort:      5000,
		CoordinatorGRPCPort:  5000, // same as the HTTP port: no second rule
		CoordinatorSGID:      "sg-coord",
		AmiID:                "ami-123",
		InstanceType:         "c7i.xlarge",
		MinSize:              0,
		DesiredCapacity:      1,
		MaxSize:              2,
	}
	callCount := 0
	c := command{
		out: &out,
		createResource: func(ctx context.Context, r *cloud.Resource) error {
			callCount++
			r.Identifier = fmt.Sprintf("resource-%d", callCount)
			return nil
		},
		writeState: func(st *fabricastate.State) error { return nil },
	}
	err := c.applyCreate(context.Background(), st, plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 5 base + HTTP ingress only (a duplicate rule for the same port would
	// be rejected by AWS)
	if callCount != 6 {
		t.Errorf("want 6 create calls (gRPC skipped when ports match), got %d", callCount)
	}
}

func TestPrintDryRun_WithScaling(t *testing.T) {
	var out bytes.Buffer
	plan := &horde.AgentsCreatePlan{
		ASGName:              "asg-test",
		LaunchTemplateName:   "lt-test",
		InstanceProfileName:  "profile-test",
		RoleName:             "role-test",
		SGName:               "sg-test",
		CoordinatorPrivateIP: "10.0.1.50",
		CoordinatorPort:      5000,
		AmiID:                "ami-123",
		InstanceType:         "c7i.xlarge",
		MinSize:              0,
		DesiredCapacity:      1,
		MaxSize:              2,
		ScalingEnabled:       true,
		ScaleOutThreshold:    10.0,
		ScaleInThreshold:     2.0,
		ScaleInCooldown:      120,
		MetricName:           "ASGQueueDepth",
		MetricNamespace:      "Fabrica/HordeAgents",
		ScaleOutAlarmName:    "alarm-out",
		ScaleInAlarmName:     "alarm-in",
		ScaleOutPolicyName:   "policy-out",
		ScaleInPolicyName:    "policy-in",
	}
	c := command{
		out:   &out,
		costs: fabricacost.Global,
	}
	c.printDryRun(plan)

	got := out.String()
	if !strings.Contains(got, "Scaling enabled") {
		t.Error("dry run output should mention 'Scaling enabled'")
	}
	if !strings.Contains(got, "Scale-out threshold") {
		t.Error("dry run output should mention 'Scale-out threshold'")
	}
	if !strings.Contains(got, "Scale-in threshold") {
		t.Error("dry run output should mention 'Scale-in threshold'")
	}
	if !strings.Contains(got, "Scale-out Policy") {
		t.Error("dry run output should list 'Scale-out Policy'")
	}
	if !strings.Contains(got, "Scale-in Policy") {
		t.Error("dry run output should list 'Scale-in Policy'")
	}
	if !strings.Contains(got, "Scale-out Alarm") {
		t.Error("dry run output should list 'Scale-out Alarm'")
	}
	if !strings.Contains(got, "Scale-in Alarm") {
		t.Error("dry run output should list 'Scale-in Alarm'")
	}
	if !strings.Contains(got, horde.ScalingPublisherNote) {
		t.Error("dry run output should include the scaling publisher standing line")
	}
}

func TestPrintDryRun_WithoutScaling(t *testing.T) {
	var out bytes.Buffer
	plan := &horde.AgentsCreatePlan{
		ASGName:              "asg-test",
		LaunchTemplateName:   "lt-test",
		InstanceProfileName:  "profile-test",
		RoleName:             "role-test",
		SGName:               "sg-test",
		CoordinatorPrivateIP: "10.0.1.50",
		CoordinatorPort:      5000,
		AmiID:                "ami-123",
		InstanceType:         "c7i.xlarge",
		MinSize:              0,
		DesiredCapacity:      1,
		MaxSize:              2,
		ScalingEnabled:       false,
	}
	c := command{
		out:   &out,
		costs: fabricacost.Global,
	}
	c.printDryRun(plan)

	got := out.String()
	if strings.Contains(got, "Scaling enabled") {
		t.Error("dry run output should not mention scaling when disabled")
	}
	if strings.Contains(got, "Scale-out Policy") {
		t.Error("dry run output should not list scaling policy when disabled")
	}
	if strings.Contains(got, horde.ScalingPublisherNote) {
		t.Error("dry run output should not include the scaling publisher note when disabled")
	}
}

func TestPrintApplyPlan(t *testing.T) {
	var out bytes.Buffer
	plan := &horde.AgentsCreatePlan{
		ASGName:              "asg-test",
		LaunchTemplateName:   "lt-test",
		InstanceProfileName:  "profile-test",
		RoleName:             "role-test",
		SGName:               "sg-test",
		CoordinatorPrivateIP: "10.0.1.50",
		CoordinatorPort:      5000,
		AmiID:                "ami-123",
		InstanceType:         "c7i.xlarge",
		MinSize:              0,
		DesiredCapacity:      1,
		MaxSize:              2,
	}
	c := command{out: &out}
	c.printApplyPlan(plan)

	got := out.String()
	if !strings.Contains(got, "asg-test") {
		t.Error("apply plan should mention ASG name")
	}
	if !strings.Contains(got, "0/1/2") {
		t.Error("apply plan should show capacity")
	}
}

func TestPrintPostCreate(t *testing.T) {
	var out bytes.Buffer
	plan := &horde.AgentsCreatePlan{
		ASGName:              "asg-test",
		LaunchTemplateName:   "lt-test",
		CoordinatorPrivateIP: "10.0.1.50",
		CoordinatorPort:      5000,
		MinSize:              0,
		DesiredCapacity:      1,
		MaxSize:              2,
	}
	c := command{out: &out}
	c.printPostCreate(plan, "asg-created")

	got := out.String()
	if !strings.Contains(got, "asg-created") {
		t.Error("post create should mention ASG ID")
	}
	if !strings.Contains(got, "Systems Manager") {
		t.Error("post create should mention SSM access")
	}
}

func TestPrintPostCreate_WithScaling(t *testing.T) {
	var out bytes.Buffer
	plan := &horde.AgentsCreatePlan{
		ASGName:              "asg-test",
		LaunchTemplateName:   "lt-test",
		CoordinatorPrivateIP: "10.0.1.50",
		CoordinatorPort:      5000,
		MinSize:              0,
		DesiredCapacity:      1,
		MaxSize:              2,
		ScalingEnabled:       true,
		MetricName:           "ASGQueueDepth",
		MetricNamespace:      "Fabrica/HordeAgents",
	}
	c := command{out: &out}
	c.printPostCreate(plan, "asg-created")

	got := out.String()
	if !strings.Contains(got, horde.ScalingPublisherNote) {
		t.Error("post create should include the scaling publisher standing line")
	}
	if !strings.Contains(got, "ASGQueueDepth") {
		t.Error("post create should mention metric name")
	}
}
