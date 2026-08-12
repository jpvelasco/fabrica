package horde

import (
	"encoding/json"
	"testing"
)

func newTestAgentsPlan() *AgentsCreatePlan {
	return &AgentsCreatePlan{
		Account:              "123456789012",
		Region:               "us-east-1",
		AmiID:                "ami-agent123",
		InstanceType:         "c7i.xlarge",
		MinSize:              0,
		DesiredCapacity:      1,
		MaxSize:              2,
		VPCID:                "vpc-test",
		SubnetID:             "subnet-test",
		CoordinatorPrivateIP: "10.0.1.10",
		CoordinatorPort:      5000,
		CoordinatorSGID:      "sg-coord123",
		SGName:               "fabrica-horde-agents-sg",
		RoleName:             "fabrica-horde-agents-role",
		InstanceProfileName:  "fabrica-horde-agents-profile",
		LaunchTemplateName:   "fabrica-horde-agents-lt",
		ASGName:              "fabrica-horde-agents-asg",
	}
}

func TestAgentSGDesiredState(t *testing.T) {
	plan := newTestAgentsPlan()
	ds, err := AgentSGDesiredState(plan)
	if err != nil {
		t.Fatalf("AgentSGDesiredState: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(ds, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if doc["GroupName"] != plan.SGName {
		t.Errorf("GroupName = %v, want %s", doc["GroupName"], plan.SGName)
	}
	if doc["VpcId"] != plan.VPCID {
		t.Errorf("VpcId = %v, want %s", doc["VpcId"], plan.VPCID)
	}

	// Check ingress rules include coordinator SG reference.
	ingressRaw, ok := doc["SecurityGroupIngress"].([]any)
	if !ok || len(ingressRaw) != 1 {
		t.Fatalf("expected 1 ingress rule, got %d", len(ingressRaw))
	}
	ingress, ok := ingressRaw[0].(map[string]any)
	if !ok {
		t.Fatal("ingress rule is not a map")
	}
	if ingress["SourceSecurityGroupId"] != plan.CoordinatorSGID {
		t.Errorf("SourceSecurityGroupId = %v, want %s", ingress["SourceSecurityGroupId"], plan.CoordinatorSGID)
	}
}

func TestAgentSGDesiredStateNoCoordinatorSG(t *testing.T) {
	plan := newTestAgentsPlan()
	plan.CoordinatorSGID = ""
	ds, err := AgentSGDesiredState(plan)
	if err != nil {
		t.Fatalf("AgentSGDesiredState: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(ds, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// No ingress rules when coordinator SG is not set.
	if _, has := doc["SecurityGroupIngress"]; has {
		t.Error("should not have SecurityGroupIngress when coordinator SG is empty")
	}
}

func TestAgentRoleDesiredState(t *testing.T) {
	plan := newTestAgentsPlan()
	ds, err := AgentRoleDesiredState(plan)
	if err != nil {
		t.Fatalf("AgentRoleDesiredState: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(ds, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if doc["RoleName"] != plan.RoleName {
		t.Errorf("RoleName = %v, want %s", doc["RoleName"], plan.RoleName)
	}

	// Check SSM policy is attached.
	policiesRaw, ok := doc["ManagedPolicyArns"].([]any)
	if !ok {
		t.Fatal("ManagedPolicyArns not found")
	}
	found := false
	for _, p := range policiesRaw {
		if s, ok := p.(string); ok && s == "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore" {
			found = true
			break
		}
	}
	if !found {
		t.Error("SSM managed policy not found in ManagedPolicyArns")
	}
}

func TestAgentInstanceProfileDesiredState(t *testing.T) {
	plan := newTestAgentsPlan()
	ds, err := AgentInstanceProfileDesiredState(plan)
	if err != nil {
		t.Fatalf("AgentInstanceProfileDesiredState: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(ds, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if doc["InstanceProfileName"] != plan.InstanceProfileName {
		t.Errorf("InstanceProfileName = %v, want %s", doc["InstanceProfileName"], plan.InstanceProfileName)
	}
	roles, ok := doc["Roles"].([]any)
	if !ok || len(roles) != 1 {
		t.Fatalf("expected 1 role, got %d", len(roles))
	}
	if roles[0] != plan.RoleName {
		t.Errorf("Role = %v, want %s", roles[0], plan.RoleName)
	}
}

func TestLaunchTemplateDesiredState(t *testing.T) {
	plan := newTestAgentsPlan()
	ds, err := LaunchTemplateDesiredState(plan, "sg-agent123", "dXNlci1kYXRh")
	if err != nil {
		t.Fatalf("LaunchTemplateDesiredState: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(ds, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if doc["LaunchTemplateName"] != plan.LaunchTemplateName {
		t.Errorf("LaunchTemplateName = %v, want %s", doc["LaunchTemplateName"], plan.LaunchTemplateName)
	}

	data, ok := doc["LaunchTemplateData"].(map[string]any)
	if !ok {
		t.Fatal("LaunchTemplateData not found")
	}
	if data["ImageId"] != plan.AmiID {
		t.Errorf("ImageId = %v, want %s", data["ImageId"], plan.AmiID)
	}
	if data["InstanceType"] != plan.InstanceType {
		t.Errorf("InstanceType = %v, want %s", data["InstanceType"], plan.InstanceType)
	}
}

func TestASGDesiredState(t *testing.T) {
	plan := newTestAgentsPlan()
	ds, err := ASGDesiredState(plan, "lt-12345")
	if err != nil {
		t.Fatalf("ASGDesiredState: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(ds, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if doc["AutoScalingGroupName"] != plan.ASGName {
		t.Errorf("AutoScalingGroupName = %v, want %s", doc["AutoScalingGroupName"], plan.ASGName)
	}
	if doc["MinSize"] != "0" {
		t.Errorf("MinSize = %v, want 0", doc["MinSize"])
	}
	if doc["MaxSize"] != "2" {
		t.Errorf("MaxSize = %v, want 2", doc["MaxSize"])
	}
	if doc["DesiredCapacity"] != "1" {
		t.Errorf("DesiredCapacity = %v, want 1", doc["DesiredCapacity"])
	}

	lt, ok := doc["LaunchTemplate"].(map[string]any)
	if !ok {
		t.Fatal("LaunchTemplate not found")
	}
	if lt["LaunchTemplateId"] != "lt-12345" {
		t.Errorf("LaunchTemplateId = %v, want lt-12345", lt["LaunchTemplateId"])
	}
}
