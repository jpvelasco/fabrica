package workstation

import (
	"context"
	"encoding/json"
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
	plan.RootDeviceName = "/dev/sda1"
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

// TestInstanceDesiredStateRootVolumeEncrypted pins #507: the workstation root
// volume (/dev/sda1) is requested with Encrypted=true, so it is encrypted even
// when the AMI snapshot is not and account EBS default encryption is off.
func TestInstanceDesiredStateRootVolumeEncrypted(t *testing.T) {
	plan := testPlan(t)
	raw, err := InstanceDesiredState(plan, "test-security-group", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	bdms, ok := doc["BlockDeviceMappings"].([]any)
	if !ok || len(bdms) != 1 {
		t.Fatalf("BlockDeviceMappings = %v, want 1 entry", doc["BlockDeviceMappings"])
	}
	m0 := bdms[0].(map[string]any)
	if m0["DeviceName"] != "/dev/sda1" {
		t.Errorf("DeviceName = %v, want /dev/sda1", m0["DeviceName"])
	}
	ebs := m0["Ebs"].(map[string]any)
	if enc, ok := ebs["Encrypted"].(bool); !ok || !enc {
		t.Errorf("Ebs.Encrypted = %v, want true", ebs["Encrypted"])
	}
}

// TestInstanceDesiredStateEncryptsTheAMIRootDevice pins #507 for AMIs whose
// root is not /dev/sda1: the encrypted mapping lands on the AMI's own root
// device (an Amazon Linux style /dev/xvda here), so the real root volume is
// encrypted and no second, empty volume is attached on /dev/sda1.
func TestInstanceDesiredStateEncryptsTheAMIRootDevice(t *testing.T) {
	plan := testPlan(t)
	plan.RootDeviceName = "/dev/xvda"
	raw, err := InstanceDesiredState(plan, "test-security-group", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	bdms := ec2state.UnmarshalDesiredState(t, raw)["BlockDeviceMappings"].([]any)
	if len(bdms) != 1 {
		t.Fatalf("BlockDeviceMappings = %v, want only the root", bdms)
	}
	m0 := bdms[0].(map[string]any)
	if m0["DeviceName"] != "/dev/xvda" {
		t.Errorf("DeviceName = %v, want /dev/xvda", m0["DeviceName"])
	}
	if enc, ok := m0["Ebs"].(map[string]any)["Encrypted"].(bool); !ok || !enc {
		t.Errorf("Ebs.Encrypted = %v, want true", m0["Ebs"])
	}
}

// TestInstanceDesiredStateRefusesUnknownRootDevice pins the fail-closed rule:
// with no (or a malformed) root device there is no launch document at all,
// never one that falls back to a default device.
func TestInstanceDesiredStateRefusesUnknownRootDevice(t *testing.T) {
	for _, dev := range []string{"", "sda1", "/dev/nvme0n1", "/dev/sda1; rm", "/dev/"} {
		t.Run(dev, func(t *testing.T) {
			plan := testPlan(t)
			plan.RootDeviceName = dev
			raw, err := InstanceDesiredState(plan, "test-security-group", "dXNlcmRhdGE=")
			if err == nil {
				t.Fatalf("InstanceDesiredState(%q) = %s, want an error", dev, raw)
			}
			if !strings.Contains(err.Error(), "encrypted root volume") {
				t.Fatalf("err = %v", err)
			}
		})
	}
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
		InstanceProfileName: "fabrica-workstation-profile", RootDeviceName: "/dev/sda1",
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

// TestInstanceDesiredStateStampsCreateNonce pins the per-create identity tag:
// the SDK launch path matches adoption on it and derives the RunInstances
// ClientToken from it.
func TestInstanceDesiredStateStampsCreateNonce(t *testing.T) {
	plan := testPlan(t)
	plan.CreateNonce = "0123456789abcdef0123456789abcdef"
	raw, err := InstanceDesiredState(plan, "sg-test123", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	got := ec2state.ParseTags(t, ec2state.UnmarshalDesiredState(t, raw)["Tags"].([]any))["FabricaCreateNonce"]
	if got != plan.CreateNonce {
		t.Errorf("FabricaCreateNonce tag = %q, want %q", got, plan.CreateNonce)
	}
	// An empty nonce (a plan built before the create layer mints one) omits
	// the tag entirely.
	blank := testPlan(t)
	blankRaw, err := InstanceDesiredState(blank, "sg-test123", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	if _, present := ec2state.ParseTags(t, ec2state.UnmarshalDesiredState(t, blankRaw)["Tags"].([]any))["FabricaCreateNonce"]; present {
		t.Error("FabricaCreateNonce tag must be omitted for an empty nonce")
	}
}

func TestRoleDesiredState_SSMManagedPolicy(t *testing.T) {
	plan := &CreatePlan{RoleName: "fabrica-workstation-role", Account: "123456789012", Region: "us-east-1"}
	// The per-create name is minted by the create layer; the role reads it.
	paramName, err := SessionPasswordParameterFor("abc123def4567890abc123def4567890")
	if err != nil {
		t.Fatal(err)
	}
	plan.SessionPasswordParameter = paramName
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
	if len(policies) != 2 {
		t.Fatalf("Policies len = %d, want 2 (SSM output + DCV password)", len(policies))
	}
	if pm := policies[0].(map[string]any); pm["PolicyName"] != "fabrica-ssm-output" {
		t.Errorf("Policies[0] = %v, want fabrica-ssm-output", pm["PolicyName"])
	}
	passwordPolicy := policies[1].(map[string]any)
	if passwordPolicy["PolicyName"] != "fabrica-workstation-dcv-password" {
		t.Errorf("Policies[1] = %v, want fabrica-workstation-dcv-password", passwordPolicy["PolicyName"])
	}
	docJSON, err := json.Marshal(passwordPolicy)
	if err != nil {
		t.Fatal(err)
	}
	wantARN := "arn:aws:ssm:us-east-1:123456789012:parameter/fabrica/workstation/abc123def4567890abc123def4567890/dcv-password"
	if !strings.Contains(string(docJSON), wantARN) {
		t.Errorf("password policy = %s, want ARN %s", docJSON, wantARN)
	}
	if strings.Contains(string(docJSON), "\"*\"") || strings.Contains(string(docJSON), "parameter/*") {
		t.Errorf("password policy must be the one parameter ARN, got %s", docJSON)
	}

	if got := ec2state.ParseTags(t, doc["Tags"].([]any))["FabricaModule"]; got != "workstation" {
		t.Errorf("role FabricaModule = %q, want workstation", got)
	}
}

func TestRoleDesiredStateGuardrails(t *testing.T) {
	plan := &CreatePlan{
		RoleName:                 "fabrica-workstation-role",
		InstanceProfileName:      "fabrica-workstation-profile",
		Account:                  "123456789012",
		Region:                   "us-east-1",
		SessionPasswordParameter: "/fabrica/workstation/abcdef0123456789abcdef0123456789/dcv-password",
		IAM: config.IAM{
			Path:                "/workstation/",
			PermissionsBoundary: "arn:aws:iam::123456789012:policy/fabrica-boundary",
		},
	}
	raw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatal(err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	if doc["Path"] != "/workstation/" {
		t.Errorf("Path = %v", doc["Path"])
	}
	if doc["PermissionsBoundary"] != plan.IAM.PermissionsBoundary {
		t.Errorf("PermissionsBoundary = %v", doc["PermissionsBoundary"])
	}
	profile, err := InstanceProfileDesiredState(plan)
	if err != nil {
		t.Fatal(err)
	}
	pdoc := ec2state.UnmarshalDesiredState(t, profile)
	if pdoc["Path"] != "/workstation/" {
		t.Errorf("profile Path = %v", pdoc["Path"])
	}
	if _, ok := pdoc["PermissionsBoundary"]; ok {
		t.Fatal("instance profile must not carry a permissions boundary")
	}
}

func TestRoleDesiredStateRequiresRegionAndAccount(t *testing.T) {
	const paramName = "/fabrica/workstation/abc123def4567890abc123def4567890/dcv-password"
	_, err := RoleDesiredState(&CreatePlan{RoleName: "fabrica-workstation-role", Account: "123456789012", SessionPasswordParameter: paramName})
	if err == nil {
		t.Fatal("expected error when region is empty")
	}
	_, err = RoleDesiredState(&CreatePlan{RoleName: "fabrica-workstation-role", Region: "us-east-1", SessionPasswordParameter: paramName})
	if err == nil {
		t.Fatal("expected error when account is empty")
	}
	// Without a recorded per-create name the ARN cannot be scoped, so the
	// role itself cannot be built.
	_, err = RoleDesiredState(&CreatePlan{RoleName: "fabrica-workstation-role", Region: "us-east-1", Account: "123456789012"})
	if err == nil {
		t.Fatal("expected error when the session-password parameter name is empty")
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
