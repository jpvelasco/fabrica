package workstation

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/ec2state"
)

func testPlan(t *testing.T) *CreatePlan {
	t.Helper()
	plan, err := NewCreatePlan(context.Background(), config.WorkstationConfig{
		AmiID:    "ami-abc123",
		VPCId:    "vpc-test",
		SubnetId: "subnet-test",
	}, "123456789012", "us-east-1", nil, "", "")
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	return plan
}

func TestSGDesiredStateFields(t *testing.T) {
	plan := testPlan(t)
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("SGDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	ec2state.AssertStringField(t, doc, "GroupName", plan.SGName)
	ec2state.AssertStringField(t, doc, "VpcId", plan.VPCID)
	ec2state.AssertIngressCidr(t, doc, 1, plan.AllowedCIDR)
}

func TestSGDesiredStateManagedByTag(t *testing.T) {
	plan := testPlan(t)
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("SGDesiredState: %v", err)
	}
	ec2state.AssertManagedByTag(t, raw)
}

func TestInstanceDesiredStateFields(t *testing.T) {
	plan := testPlan(t)
	raw, err := InstanceDesiredState(plan, "sg-test123", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	ec2state.AssertStringField(t, doc, "ImageId", plan.AmiID)
	ec2state.AssertStringField(t, doc, "InstanceType", plan.InstanceType)
	ec2state.AssertStringField(t, doc, "SubnetId", plan.SubnetID)
	ec2state.AssertSGID(t, doc, "sg-test123")
	ec2state.AssertIMDSv2(t, doc)
}

func TestInstanceDesiredStateVolume(t *testing.T) {
	plan := testPlan(t)
	raw, err := InstanceDesiredState(plan, "sg-test123", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	ec2state.AssertEBS(t, doc, plan.VolumeSize, true)
}

// TestDesiredStatesStampFabricaModule guards fix #329: workstation resources
// must carry FabricaModule=workstation.
func TestDesiredStatesStampFabricaModule(t *testing.T) {
	plan := &CreatePlan{SGName: "fabrica-workstation-sg", VPCID: "vpc-x", AllowedCIDR: "10.0.0.0/32", DCVPort: 8443}

	sgRaw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("SGDesiredState: %v", err)
	}
	if got := ec2state.ParseTags(t, ec2state.UnmarshalDesiredState(t, sgRaw)["Tags"].([]any))["FabricaModule"]; got != "workstation" {
		t.Errorf("SG FabricaModule = %q, want workstation", got)
	}

	instRaw, err := InstanceDesiredState(&CreatePlan{
		AmiID: "ami-1", InstanceType: "g4dn.xlarge", SubnetID: "subnet-abc",
		InstanceName: "fabrica-workstation", VolumeSize: 100,
		InstanceProfileName: "fabrica-workstation-profile",
	}, "sg-123", "ud")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	if got := ec2state.ParseTags(t, ec2state.UnmarshalDesiredState(t, instRaw)["Tags"].([]any))["FabricaModule"]; got != "workstation" {
		t.Errorf("instance FabricaModule = %q, want workstation", got)
	}
}

func TestInstanceDesiredStateAttachesInstanceProfile(t *testing.T) {
	plan := testPlan(t)
	raw, err := InstanceDesiredState(plan, "sg-test123", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	if doc["IamInstanceProfile"] != plan.InstanceProfileName {
		t.Errorf("IamInstanceProfile = %v, want %q", doc["IamInstanceProfile"], plan.InstanceProfileName)
	}
}

func TestRoleDesiredState_SSMManagedPolicy(t *testing.T) {
	plan := &CreatePlan{RoleName: "fabrica-workstation-role", Account: "123456789012", Region: "us-east-1"}
	raw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	if doc["RoleName"] != "fabrica-workstation-role" {
		t.Errorf("RoleName = %v, want fabrica-workstation-role", doc["RoleName"])
	}

	arns, ok := doc["ManagedPolicyArns"].([]any)
	if !ok || len(arns) != 1 {
		t.Fatalf("ManagedPolicyArns: expected 1 entry, got %v", doc["ManagedPolicyArns"])
	}
	if !strings.Contains(arns[0].(string), "AmazonSSMManagedInstanceCore") {
		t.Errorf("ManagedPolicyArns[0] = %q, want AmazonSSMManagedInstanceCore", arns[0])
	}

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

	if got := ec2state.ParseTags(t, doc["Tags"].([]any))["FabricaModule"]; got != "workstation" {
		t.Errorf("role FabricaModule = %q, want workstation", got)
	}
}

func TestInstanceProfileDesiredState(t *testing.T) {
	plan := &CreatePlan{RoleName: "fabrica-workstation-role", InstanceProfileName: "fabrica-workstation-profile"}
	raw, err := InstanceProfileDesiredState(plan)
	if err != nil {
		t.Fatalf("InstanceProfileDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	if doc["InstanceProfileName"] != "fabrica-workstation-profile" {
		t.Errorf("InstanceProfileName = %v, want fabrica-workstation-profile", doc["InstanceProfileName"])
	}

	roles, ok := doc["Roles"].([]any)
	if !ok || len(roles) != 1 || roles[0] != "fabrica-workstation-role" {
		t.Errorf("Roles = %v, want [fabrica-workstation-role]", doc["Roles"])
	}
}
