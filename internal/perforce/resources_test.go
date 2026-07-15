package perforce

import (
	"encoding/json"
	"strings"
	"testing"
)

// parseTags converts the Tags []map[string]any from Cloud Control JSON into a flat map.
func parseTags(v any) map[string]string {
	tags := v.([]any)
	m := make(map[string]string, len(tags))
	for _, tag := range tags {
		entry := tag.(map[string]any)
		m[entry["Key"].(string)] = entry["Value"].(string)
	}
	return m
}

func TestSGDesiredState_Port1666(t *testing.T) {
	plan := &CreatePlan{SGName: "fabrica-perforce-sg", VPCID: "vpc-test"}
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
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

func TestSGDesiredState_VPCAndName(t *testing.T) {
	plan := &CreatePlan{SGName: "fabrica-perforce-sg", VPCID: "vpc-abc123"}
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc["VpcId"] != "vpc-abc123" {
		t.Errorf("VpcId = %v, want vpc-abc123", doc["VpcId"])
	}
	if doc["GroupName"] != "fabrica-perforce-sg" {
		t.Errorf("GroupName = %v, want fabrica-perforce-sg", doc["GroupName"])
	}
}

func TestSGDesiredState_ManagedByTag(t *testing.T) {
	plan := &CreatePlan{SGName: "fabrica-perforce-sg", VPCID: "vpc-x"}
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	tagMap := parseTags(doc["Tags"])
	if tagMap["ManagedBy"] != "fabrica" {
		t.Errorf("ManagedBy tag = %q, want fabrica", tagMap["ManagedBy"])
	}
	if tagMap["Name"] != "fabrica-perforce-sg" {
		t.Errorf("Name tag = %q, want fabrica-perforce-sg", tagMap["Name"])
	}
}

func TestInstanceDesiredState_CoreFields(t *testing.T) {
	plan := &CreatePlan{
		InstanceType: "m5.xlarge",
		SubnetID:     "subnet-abc",
		InstanceName: "fabrica-perforce",
		VolumeSize:   500,
	}
	raw, err := InstanceDesiredState(plan, "sg-123", "userdata-b64", "fabrica-perforce-profile")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc["InstanceType"] != "m5.xlarge" {
		t.Errorf("InstanceType = %v", doc["InstanceType"])
	}
	if doc["SubnetId"] != "subnet-abc" {
		t.Errorf("SubnetId = %v", doc["SubnetId"])
	}
	if doc["UserData"] != "userdata-b64" {
		t.Errorf("UserData = %v", doc["UserData"])
	}
	sgIDs := doc["SecurityGroupIds"].([]any)
	if len(sgIDs) != 1 || sgIDs[0] != "sg-123" {
		t.Errorf("SecurityGroupIds = %v, want [sg-123]", sgIDs)
	}
	prof := doc["IamInstanceProfile"].(map[string]any)
	if prof["Name"] != "fabrica-perforce-profile" {
		t.Errorf("IamInstanceProfile = %v", prof)
	}
}

func TestInstanceDesiredState_EBSNotDeletedOnTermination(t *testing.T) {
	plan := &CreatePlan{InstanceType: "m5.xlarge", VolumeSize: 750, InstanceName: "fabrica-perforce"}
	raw, err := InstanceDesiredState(plan, "sg-x", "ud", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
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

func TestInstanceDesiredState_ARNProfile(t *testing.T) {
	plan := &CreatePlan{InstanceType: "m5.xlarge", VolumeSize: 500, InstanceName: "fabrica-perforce", SubnetID: "subnet-1"}
	raw, err := InstanceDesiredState(plan, "sg-1", "ud", "arn:aws:iam::123:instance-profile/p")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	prof := doc["IamInstanceProfile"].(map[string]any)
	if prof["Arn"] == nil {
		t.Fatalf("expected Arn profile: %v", prof)
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
	raw, err := InstanceDesiredState(plan, "sg-x", "ud", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	meta := doc["MetadataOptions"].(map[string]any)
	if meta["HttpTokens"] != "required" {
		t.Errorf("HttpTokens = %v, want required (IMDSv2 must be enforced)", meta["HttpTokens"])
	}
}

func TestInstanceDesiredState_ManagedByTag(t *testing.T) {
	plan := &CreatePlan{InstanceType: "m5.xlarge", VolumeSize: 500, InstanceName: "fabrica-perforce"}
	raw, err := InstanceDesiredState(plan, "sg-x", "ud", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	tagMap := parseTags(doc["Tags"])
	if tagMap["ManagedBy"] != "fabrica" {
		t.Errorf("ManagedBy tag = %q, want fabrica", tagMap["ManagedBy"])
	}
	if tagMap["Name"] != "fabrica-perforce" {
		t.Errorf("Name tag = %q, want fabrica-perforce", tagMap["Name"])
	}
}
