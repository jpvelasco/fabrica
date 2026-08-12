package lore

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/ec2state"
)

func TestSGDesiredStateShape(t *testing.T) {
	plan := &CreatePlan{
		SGName:      "fabrica-lore-sg",
		VPCID:       "vpc-abc123",
		GRPCPort:    41337,
		HTTPPort:    41339,
		AllowedCIDR: "10.0.0.0/8",
	}
	raw, err := SGDesiredState(plan)
	if err != nil {
		t.Fatalf("SGDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	if doc["GroupName"] != "fabrica-lore-sg" {
		t.Errorf("GroupName = %v", doc["GroupName"])
	}

	ingress, ok := doc["SecurityGroupIngress"].([]any)
	if !ok {
		t.Fatalf("SecurityGroupIngress is not an array")
	}
	if len(ingress) != 3 {
		t.Fatalf("SecurityGroupIngress len = %d, want 3", len(ingress))
	}

	want := []struct {
		proto string
		port  float64
	}{
		{"tcp", 41337},
		{"udp", 41337},
		{"tcp", 41339},
	}
	for i, w := range want {
		r := ingress[i].(map[string]any)
		if r["IpProtocol"] != w.proto {
			t.Errorf("ingress[%d].IpProtocol = %v, want %s", i, r["IpProtocol"], w.proto)
		}
		if r["FromPort"] != w.port || r["ToPort"] != w.port {
			t.Errorf("ingress[%d] ports = %v/%v, want %v", i, r["FromPort"], r["ToPort"], w.port)
		}
		if r["CidrIp"] != "10.0.0.0/8" {
			t.Errorf("ingress[%d].CidrIp = %v", i, r["CidrIp"])
		}
	}

	// Ensure UDP rule is present (first Fabrica module with UDP).
	var sawUDP bool
	for _, rule := range ingress {
		r := rule.(map[string]any)
		if r["IpProtocol"] == "udp" {
			sawUDP = true
		}
	}
	if !sawUDP {
		t.Error("expected a UDP ingress rule for QUIC")
	}

	ec2state.AssertManagedByTag(t, raw)
}

func TestInstanceDesiredStateShape(t *testing.T) {
	plan := &CreatePlan{
		AmiID:        "ami-lore1",
		InstanceType: "m5.xlarge",
		SubnetID:     "subnet-1",
		VolumeSize:   500,
		InstanceName: "fabrica-lore",
	}
	raw, err := InstanceDesiredState(plan, "sg-abc", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	ec2state.AssertStringField(t, doc, "ImageId", "ami-lore1")
	ec2state.AssertStringField(t, doc, "InstanceType", "m5.xlarge")
	ec2state.AssertSGID(t, doc, "sg-abc")

	bdm := doc["BlockDeviceMappings"].([]any)
	ebs := bdm[0].(map[string]any)["Ebs"].(map[string]any)
	if ebs["VolumeSize"] != float64(500) {
		t.Errorf("VolumeSize = %v", ebs["VolumeSize"])
	}
	if ebs["DeleteOnTermination"] != true {
		t.Errorf("DeleteOnTermination = %v, want true (destroy deletes store with instance)", ebs["DeleteOnTermination"])
	}
}

func TestInstanceDesiredStateWithS3Store(t *testing.T) {
	plan := &CreatePlan{
		AmiID:               "ami-lore1",
		InstanceType:        "m5.xlarge",
		SubnetID:            "subnet-1",
		VolumeSize:          500,
		InstanceName:        "fabrica-lore",
		StoreBackend:        StoreBackendS3,
		InstanceProfileName: "fabrica-lore-profile",
	}
	raw, err := InstanceDesiredState(plan, "sg-abc", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	// Verify IAM instance profile is set when S3 store is enabled.
	if doc["IamInstanceProfile"] != "fabrica-lore-profile" {
		t.Errorf("IamInstanceProfile = %v, want fabrica-lore-profile", doc["IamInstanceProfile"])
	}
}

func TestInstanceDesiredStateWithoutS3Store(t *testing.T) {
	plan := &CreatePlan{
		AmiID:        "ami-lore1",
		InstanceType: "m5.xlarge",
		SubnetID:     "subnet-1",
		VolumeSize:   500,
		InstanceName: "fabrica-lore",
		StoreBackend: StoreBackendLocal,
	}
	raw, err := InstanceDesiredState(plan, "sg-abc", "dXNlcmRhdGE=")
	if err != nil {
		t.Fatalf("InstanceDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	// Verify IAM instance profile is NOT set for local store.
	if _, hasProfile := doc["IamInstanceProfile"]; hasProfile {
		t.Error("IamInstanceProfile should not be set for local store backend")
	}
}

func TestBucketDesiredStateShape(t *testing.T) {
	plan := &CreatePlan{
		StoreBucket:  "fabrica-lore-store-123-us-east-1",
		StoreBackend: StoreBackendS3,
	}
	raw, err := BucketDesiredState(plan)
	if err != nil {
		t.Fatalf("BucketDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	if doc["BucketName"] != "fabrica-lore-store-123-us-east-1" {
		t.Errorf("BucketName = %v", doc["BucketName"])
	}

	// Verify public access block.
	pab, ok := doc["PublicAccessBlockConfiguration"].(map[string]any)
	if !ok {
		t.Fatal("PublicAccessBlockConfiguration not found")
	}
	if pab["BlockPublicAcls"] != true {
		t.Error("BlockPublicAcls should be true")
	}
	if pab["BlockPublicPolicy"] != true {
		t.Error("BlockPublicPolicy should be true")
	}

	// Verify encryption.
	enc, ok := doc["BucketEncryption"].(map[string]any)
	if !ok {
		t.Fatal("BucketEncryption not found")
	}
	if enc == nil {
		t.Error("BucketEncryption should not be nil")
	}

	// Verify versioning.
	ver, ok := doc["VersioningConfiguration"].(map[string]any)
	if !ok {
		t.Fatal("VersioningConfiguration not found")
	}
	if ver["Status"] != "Enabled" {
		t.Errorf("Versioning Status = %v, want Enabled", ver["Status"])
	}

	ec2state.AssertManagedByTag(t, raw)
}

func TestRoleDesiredStateShape(t *testing.T) {
	plan := &CreatePlan{
		RoleName:    "fabrica-lore-role",
		StoreBucket: "fabrica-lore-store-123-us-east-1",
	}
	raw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	if doc["RoleName"] != "fabrica-lore-role" {
		t.Errorf("RoleName = %v", doc["RoleName"])
	}

	// Verify assume role policy allows EC2.
	arp, ok := doc["AssumeRolePolicyDocument"].(map[string]any)
	if !ok {
		t.Fatal("AssumeRolePolicyDocument not found")
	}
	stmts := arp["Statement"].([]any)
	if len(stmts) == 0 {
		t.Fatal("AssumeRolePolicyDocument has no statements")
	}
	stmt := stmts[0].(map[string]any)
	principal := stmt["Principal"].(map[string]any)
	if principal["Service"] != "ec2.amazonaws.com" {
		t.Errorf("Service principal = %v, want ec2.amazonaws.com", principal["Service"])
	}

	// Verify SSM managed policy is attached.
	arns, ok := doc["ManagedPolicyArns"].([]any)
	if !ok {
		t.Fatal("ManagedPolicyArns not found")
	}
	found := false
	for _, a := range arns {
		if a == "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore" {
			found = true
		}
	}
	if !found {
		t.Error("SSM managed policy not found in ManagedPolicyArns")
	}

	// Verify S3 inline policy is present.
	policies, ok := doc["Policies"].([]any)
	if !ok {
		t.Fatal("Policies not found")
	}
	if len(policies) == 0 {
		t.Error("Expected at least one inline policy (S3 bucket access)")
	}
}

func TestInstanceProfileDesiredStateShape(t *testing.T) {
	plan := &CreatePlan{
		InstanceProfileName: "fabrica-lore-profile",
		RoleName:            "fabrica-lore-role",
	}
	raw, err := InstanceProfileDesiredState(plan)
	if err != nil {
		t.Fatalf("InstanceProfileDesiredState: %v", err)
	}
	doc := ec2state.UnmarshalDesiredState(t, raw)

	if doc["InstanceProfileName"] != "fabrica-lore-profile" {
		t.Errorf("InstanceProfileName = %v", doc["InstanceProfileName"])
	}
	roles := doc["Roles"].([]any)
	if len(roles) != 1 || roles[0] != "fabrica-lore-role" {
		t.Errorf("Roles = %v, want [fabrica-lore-role]", roles)
	}
}
