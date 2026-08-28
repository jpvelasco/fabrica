package horde

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/ec2state"
)

func TestSGDesiredStateShape(t *testing.T) {
	plan := &CreatePlan{
		SGName:      "fabrica-horde-sg",
		VPCID:       "vpc-abc123",
		Port:        5000,
		GRPCPort:    5002,
		AllowedCIDR: "10.0.0.0/8",
	}
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("SGDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	if doc["GroupName"] != "fabrica-horde-sg" {
		t.Errorf("GroupName = %v, want fabrica-horde-sg", doc["GroupName"])
	}
	if doc["VpcId"] != "vpc-abc123" {
		t.Errorf("VpcId = %v, want vpc-abc123", doc["VpcId"])
	}

	ec2state.AssertIngressCidr(t, doc, 2, "10.0.0.0/8")
	ingress := doc["SecurityGroupIngress"].([]any)
	ports := []float64{5000, 5002}
	for i, rule := range ingress {
		r := rule.(map[string]any)
		if r["FromPort"] != ports[i] {
			t.Errorf("ingress[%d].FromPort = %v, want %v", i, r["FromPort"], ports[i])
		}
	}

	ec2state.AssertManagedByTag(t, raw)
}

func TestSGDesiredStateAllowedCIDRAppliedToBothPorts(t *testing.T) {
	plan := &CreatePlan{
		SGName:      "fabrica-horde-sg",
		VPCID:       "vpc-abc123",
		Port:        5000,
		GRPCPort:    5002,
		AllowedCIDR: "172.16.0.0/12",
	}
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("SGDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	ec2state.AssertIngressCidr(t, doc, 2, "172.16.0.0/12")
}

func TestInstanceDesiredStateShape(t *testing.T) {
	plan := &CreatePlan{
		InstanceName: "fabrica-horde",
		InstanceType: "m7i.xlarge",
		AmiID:        "ami-abc123",
		SubnetID:     "subnet-abc",
		VolumeSize:   100,
	}
	raw, err := InstanceDesiredState(plan, "sg-abc123", "dXNlcmRhdGE=", "")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	ec2state.AssertStringField(t, doc, "ImageId", "ami-abc123")
	ec2state.AssertStringField(t, doc, "InstanceType", "m7i.xlarge")
	ec2state.AssertStringField(t, doc, "SubnetId", "subnet-abc")
	ec2state.AssertStringField(t, doc, "UserData", "dXNlcmRhdGE=")
	ec2state.AssertSGID(t, doc, "sg-abc123")
	ec2state.AssertIMDSv2(t, doc)
	ec2state.AssertEBS(t, doc, 100, false)

	if _, has := doc["IamInstanceProfile"]; has {
		t.Error("IamInstanceProfile should not be present when empty")
	}

	tags := ec2state.ParseTags(t, doc["Tags"].([]any))
	if tags["Name"] != "fabrica-horde" {
		t.Errorf("Name tag = %q, want fabrica-horde", tags["Name"])
	}
}

func TestInstanceDesiredState_WithIAMProfile(t *testing.T) {
	plan := &CreatePlan{
		InstanceName: "fabrica-horde",
		InstanceType: "m7i.xlarge",
		AmiID:        "ami-abc123",
		SubnetID:     "subnet-abc",
		VolumeSize:   100,
	}
	raw, err := InstanceDesiredState(plan, "sg-abc123", "dXNlcmRhdGE=", "fabrica-horde-profile")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	if doc["IamInstanceProfile"] != "fabrica-horde-profile" {
		t.Errorf("IamInstanceProfile = %v, want fabrica-horde-profile", doc["IamInstanceProfile"])
	}
}

func TestRoleDesiredState_SSMManagedPolicy(t *testing.T) {
	plan := &CreatePlan{RoleName: "fabrica-horde-role"}
	raw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	if doc["RoleName"] != "fabrica-horde-role" {
		t.Errorf("RoleName = %v, want fabrica-horde-role", doc["RoleName"])
	}

	arns, ok := doc["ManagedPolicyArns"].([]any)
	if !ok || len(arns) != 1 {
		t.Fatalf("ManagedPolicyArns: expected 1 entry, got %v", doc["ManagedPolicyArns"])
	}
	if !strings.Contains(arns[0].(string), "AmazonSSMManagedInstanceCore") {
		t.Errorf("ManagedPolicyArns[0] = %q, want to contain AmazonSSMManagedInstanceCore", arns[0])
	}

	// Check trust policy allows EC2
	docMap, ok := doc["AssumeRolePolicyDocument"].(map[string]any)
	if !ok {
		t.Fatal("AssumeRolePolicyDocument is not a map")
	}
	stmts, ok := docMap["Statement"].([]any)
	if !ok || len(stmts) != 1 {
		t.Fatal("expected 1 statement in trust policy")
	}
	stmt := stmts[0].(map[string]any)
	principal, ok := stmt["Principal"].(map[string]any)
	if !ok {
		t.Fatal("Principal is not a map")
	}
	if principal["Service"] != "ec2.amazonaws.com" {
		t.Errorf("Principal.Service = %v, want ec2.amazonaws.com", principal["Service"])
	}

	// The SSM output inline policy must be attached.
	policies, ok := doc["Policies"].([]any)
	if !ok {
		t.Fatal("Policies not found in role")
	}
	if len(policies) != 1 {
		t.Fatalf("Policies len = %d, want 1 (SSM output)", len(policies))
	}
	if pm := policies[0].(map[string]any); pm["PolicyName"] != "fabrica-ssm-output" {
		t.Errorf("Policies[0] = %v, want fabrica-ssm-output", pm["PolicyName"])
	}
}

func TestInstanceProfileDesiredState(t *testing.T) {
	plan := &CreatePlan{RoleName: "fabrica-horde-role", InstanceProfileName: "fabrica-horde-profile"}
	raw, err := InstanceProfileDesiredState(plan)
	if err != nil {
		t.Fatalf("InstanceProfileDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	if doc["InstanceProfileName"] != "fabrica-horde-profile" {
		t.Errorf("InstanceProfileName = %v, want fabrica-horde-profile", doc["InstanceProfileName"])
	}

	roles, ok := doc["Roles"].([]any)
	if !ok || len(roles) != 1 || roles[0] != "fabrica-horde-role" {
		t.Errorf("Roles = %v, want [fabrica-horde-role]", doc["Roles"])
	}
}

// TestCoordinatorDesiredStatesStampFabricaModule guards fix #329: coordinator
// resources must carry FabricaModule=horde (agents already stamped their own).
func TestCoordinatorDesiredStatesStampFabricaModule(t *testing.T) {
	plan := &CreatePlan{SGName: "fabrica-horde-sg", VPCID: "vpc-x", AllowedCIDR: "10.0.0.0/32", Port: 5000, GRPCPort: 5002}

	sgRaw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("SGDesiredState: %v", err)
	}
	if got := ec2state.ParseTags(t, ec2state.UnmarshalDesiredState(t, sgRaw)["Tags"].([]any))["FabricaModule"]; got != "horde" {
		t.Errorf("SG FabricaModule = %q, want horde", got)
	}

	instRaw, err := InstanceDesiredState(&CreatePlan{
		AmiID: "ami-1", InstanceType: "m7i.2xlarge", SubnetID: "subnet-abc",
		InstanceName: "fabrica-horde", VolumeSize: 100,
	}, "sg-123", "ud", "")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	if got := ec2state.ParseTags(t, ec2state.UnmarshalDesiredState(t, instRaw)["Tags"].([]any))["FabricaModule"]; got != "horde" {
		t.Errorf("instance FabricaModule = %q, want horde", got)
	}

	roleRaw, err := RoleDesiredState(&CreatePlan{RoleName: "fabrica-horde-role"})
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	if got := ec2state.ParseTags(t, ec2state.UnmarshalDesiredState(t, roleRaw)["Tags"].([]any))["FabricaModule"]; got != "horde" {
		t.Errorf("role FabricaModule = %q, want horde", got)
	}
}
