package perforce

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/ec2state"
)

func TestSGDesiredState_Port1666(t *testing.T) {
	plan := &CreatePlan{SGName: "fabrica-perforce-sg", VPCID: "vpc-test"}
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	ingress := doc["SecurityGroupIngress"].([]any)
	if len(ingress) != 1 {
		t.Fatalf("expected 1 ingress rule, got %d", len(ingress))
	}
	rule := ingress[0].(map[string]any)
	if rule["IpProtocol"] != "tcp" {
		t.Errorf("IpProtocol = %v, want tcp", rule["IpProtocol"])
	}
	if rule["FromPort"].(float64) != 1666 {
		t.Errorf("FromPort = %v, want 1666", rule["FromPort"])
	}
	if rule["ToPort"].(float64) != 1666 {
		t.Errorf("ToPort = %v, want 1666", rule["ToPort"])
	}
}

func TestSGDesiredState_AllowedCIDR(t *testing.T) {
	plan := &CreatePlan{SGName: "fabrica-perforce-sg", VPCID: "vpc-test", AllowedCIDR: "172.16.0.0/12"}
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	ec2state.AssertIngressCidr(t, doc, 1, "172.16.0.0/12")
}

func TestSGDesiredState_VPCAndName(t *testing.T) {
	plan := &CreatePlan{SGName: "fabrica-perforce-sg", VPCID: "vpc-abc123"}
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	ec2state.AssertStringField(t, doc, "VpcId", "vpc-abc123")
	ec2state.AssertStringField(t, doc, "GroupName", "fabrica-perforce-sg")
}

func TestSGDesiredState_ManagedByTag(t *testing.T) {
	plan := &CreatePlan{SGName: "fabrica-perforce-sg", VPCID: "vpc-x"}
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ec2state.AssertManagedByTag(t, raw)
	doc := ec2state.UnmarshalDesiredState(t, raw)
	tags := ec2state.ParseTags(t, doc["Tags"].([]any))
	if tags["Name"] != "fabrica-perforce-sg" {
		t.Errorf("Name tag = %q, want fabrica-perforce-sg", tags["Name"])
	}
}

func TestInstanceDesiredState_CoreFields(t *testing.T) {
	plan := &CreatePlan{
		InstanceType: "m5.xlarge",
		SubnetID:     "subnet-abc",
		InstanceName: "fabrica-perforce",
		VolumeSize:   500,
	}
	raw, err := InstanceDesiredState(plan, "sg-123", "userdata-b64", "fabrica-perforce-profile", "ami-test123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	ec2state.AssertStringField(t, doc, "InstanceType", "m5.xlarge")
	ec2state.AssertStringField(t, doc, "SubnetId", "subnet-abc")
	ec2state.AssertStringField(t, doc, "UserData", "userdata-b64")
	ec2state.AssertSGID(t, doc, "sg-123")
	if doc["IamInstanceProfile"] != "fabrica-perforce-profile" {
		t.Errorf("IamInstanceProfile = %v, want fabrica-perforce-profile", doc["IamInstanceProfile"])
	}
}

func TestInstanceDesiredState_EBSNotDeletedOnTermination(t *testing.T) {
	plan := &CreatePlan{InstanceType: "m5.xlarge", VolumeSize: 750, InstanceName: "fabrica-perforce"}
	raw, err := InstanceDesiredState(plan, "sg-x", "ud", "", "ami-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	bdms := doc["BlockDeviceMappings"].([]any)
	if len(bdms) != 1 {
		t.Fatalf("expected 1 BDM, got %d", len(bdms))
	}
	ebs := bdms[0].(map[string]any)["Ebs"].(map[string]any)
	if ebs["VolumeSize"].(float64) != 750 {
		t.Errorf("VolumeSize = %v, want 750", ebs["VolumeSize"])
	}
	if ebs["VolumeType"] != "gp3" {
		t.Errorf("VolumeType = %v, want gp3", ebs["VolumeType"])
	}
	if ebs["DeleteOnTermination"].(bool) {
		t.Error("DeleteOnTermination must be false — data volume must survive instance termination")
	}
}

func TestRoleDesiredState_SSMManagedPolicy(t *testing.T) {
	plan := &CreatePlan{RoleName: "fabrica-perforce-role"}
	raw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	arns := doc["ManagedPolicyArns"].([]any)
	if len(arns) != 1 || !strings.Contains(arns[0].(string), "AmazonSSMManagedInstanceCore") {
		t.Errorf("ManagedPolicyArns = %v", arns)
	}
}

func TestRoleDesiredState_S3ExportPolicy(t *testing.T) {
	plan := &CreatePlan{
		RoleName:       "fabrica-perforce-role",
		BackupS3Export: true,
		BackupS3Bucket: "my-bucket",
		BackupS3Prefix: "p4/",
	}
	raw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, "s3:PutObject") || !strings.Contains(s, "my-bucket") {
		t.Fatalf("expected S3 policy in role: %s", s)
	}
}

func TestRoleDesiredState_S3ExportDefaultPrefix(t *testing.T) {
	plan := &CreatePlan{
		RoleName:       "fabrica-perforce-role",
		BackupS3Export: true,
		BackupS3Bucket: "bkt",
		BackupS3Prefix: "", // hits DefaultS3Prefix branch
	}
	raw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), DefaultS3Prefix) {
		t.Fatalf("expected default prefix in %s", raw)
	}
}

func TestInstanceDesiredState_ARNProfile(t *testing.T) {
	plan := &CreatePlan{InstanceType: "m5.xlarge", VolumeSize: 500, InstanceName: "fabrica-perforce", SubnetID: "subnet-1"}
	raw, err := InstanceDesiredState(plan, "sg-1", "ud", "arn:aws:iam::123:instance-profile/p", "ami-test")
	if err != nil {
		t.Fatal(err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	if doc["IamInstanceProfile"] != "arn:aws:iam::123:instance-profile/p" {
		t.Fatalf("expected arn profile: %v", doc["IamInstanceProfile"])
	}
}

func TestInstanceProfileDesiredState(t *testing.T) {
	plan := &CreatePlan{RoleName: "fabrica-perforce-role", InstanceProfileName: "fabrica-perforce-profile"}
	raw, err := InstanceProfileDesiredState(plan)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["InstanceProfileName"] != "fabrica-perforce-profile" {
		t.Errorf("profile name = %v", doc["InstanceProfileName"])
	}
}

func TestInstanceDesiredState_IMDSv2Required(t *testing.T) {
	plan := &CreatePlan{InstanceType: "m5.xlarge", VolumeSize: 500, InstanceName: "fabrica-perforce"}
	raw, err := InstanceDesiredState(plan, "sg-x", "ud", "", "ami-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	ec2state.AssertIMDSv2(t, doc)
}

func TestInstanceDesiredState_ManagedByTag(t *testing.T) {
	plan := &CreatePlan{InstanceType: "m5.xlarge", VolumeSize: 500, InstanceName: "fabrica-perforce"}
	raw, err := InstanceDesiredState(plan, "sg-x", "ud", "", "ami-test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ec2state.AssertManagedByTag(t, raw)
	doc := ec2state.UnmarshalDesiredState(t, raw)
	tags := ec2state.ParseTags(t, doc["Tags"].([]any))
	if tags["Name"] != "fabrica-perforce" {
		t.Errorf("Name tag = %q, want fabrica-perforce", tags["Name"])
	}
}

func TestInstanceDesiredState_ImageID(t *testing.T) {
	plan := &CreatePlan{InstanceType: "m5.xlarge", VolumeSize: 500, InstanceName: "fabrica-perforce"}
	raw, err := InstanceDesiredState(plan, "sg-x", "ud", "", "ami-ubuntu2204")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	ec2state.AssertStringField(t, doc, "ImageId", "ami-ubuntu2204")
}

func TestInstanceDesiredState_EmptyImageID(t *testing.T) {
	plan := &CreatePlan{InstanceType: "m5.xlarge", VolumeSize: 500, InstanceName: "fabrica-perforce"}
	raw, err := InstanceDesiredState(plan, "sg-x", "ud", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)
	if _, ok := doc["ImageId"]; ok {
		t.Error("ImageId should not be present when empty")
	}
}

// TestDesiredStatesStampFabricaModule guards fix #329: every perforce resource
// must carry FabricaModule=perforce so tag-based attribution works live.
func TestDesiredStatesStampFabricaModule(t *testing.T) {
	plan := &CreatePlan{SGName: "fabrica-perforce-sg", VPCID: "vpc-x", AllowedCIDR: "10.0.0.0/32"}

	sgRaw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("SGDesiredState: %v", err)
	}
	if got := ec2state.ParseTags(t, ec2state.UnmarshalDesiredState(t, sgRaw)["Tags"].([]any))["FabricaModule"]; got != "perforce" {
		t.Errorf("SG FabricaModule = %q, want perforce", got)
	}

	instRaw, err := InstanceDesiredState(&CreatePlan{
		InstanceType: "m5.xlarge", SubnetID: "subnet-abc", InstanceName: "fabrica-perforce", VolumeSize: 500,
	}, "sg-123", "ud", "", "")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	if got := ec2state.ParseTags(t, ec2state.UnmarshalDesiredState(t, instRaw)["Tags"].([]any))["FabricaModule"]; got != "perforce" {
		t.Errorf("instance FabricaModule = %q, want perforce", got)
	}

	roleRaw, err := RoleDesiredState(&CreatePlan{RoleName: "fabrica-perforce-role"})
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	if got := ec2state.ParseTags(t, ec2state.UnmarshalDesiredState(t, roleRaw)["Tags"].([]any))["FabricaModule"]; got != "perforce" {
		t.Errorf("role FabricaModule = %q, want perforce", got)
	}
}
