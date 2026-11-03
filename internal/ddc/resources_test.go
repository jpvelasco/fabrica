package ddc

import (
	"encoding/json"
	"strings"
	"testing"
)

func testPlan(backend string) *SetupPlan {
	return &SetupPlan{
		Account:              "123",
		Region:               "us-east-1",
		Backend:              backend,
		AmiID:                "ami-ddc",
		ScyllaAmiID:          "ami-scylla",
		InstanceType:         "m7i.xlarge",
		VolumeSize:           500,
		ScyllaInstanceType:   "i4i.large",
		RootDeviceName:       "/dev/sda1",
		ScyllaRootDeviceName: "/dev/sda1",
		ScyllaVolumeSize:     500,
		PublicPort:           80,
		InternalPort:         8080,
		AllowedCIDR:          "10.0.0.0/8",
		InternalCIDR:         "10.0.0.0/8",
		VPCID:                "vpc-1",
		SubnetID:             "subnet-1",
		Bucket:               "fabrica-ddc-test",
		Namespace:            "deriveddatacache",
		SGName:               "fabrica-ddc-sg",
		InstanceName:         "fabrica-ddc",
		ScyllaInstanceName:   "fabrica-ddc-scylla",
		RoleName:             "fabrica-ddc-role",
		InstanceProfileName:  "fabrica-ddc-profile",
	}
}

func TestSGDesiredStateZen(t *testing.T) {
	raw, err := SGDesiredState(testPlan(BackendZen))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, "public API") || strings.Contains(s, "9042") {
		t.Fatalf("unexpected SG: %s", s)
	}
}

func TestSGDesiredStateScylla(t *testing.T) {
	raw, err := SGDesiredState(testPlan(BackendScylla))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "9042") {
		t.Fatalf("missing scylla port: %s", raw)
	}
}

func TestBucketDesiredState(t *testing.T) {
	raw, err := BucketDesiredState(testPlan(BackendZen))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["BucketName"] != "fabrica-ddc-test" {
		t.Fatalf("BucketName = %v", doc["BucketName"])
	}
}

func TestRoleAndProfile(t *testing.T) {
	role, err := RoleDesiredState(testPlan(BackendZen))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(role), "fabrica-ddc-test") {
		t.Fatalf("role missing bucket: %s", role)
	}
	// Both the S3 bucket policy and the SSM output policy must be attached.
	var doc map[string]any
	if err := json.Unmarshal(role, &doc); err != nil {
		t.Fatal(err)
	}
	policies, ok := doc["Policies"].([]any)
	if !ok {
		t.Fatal("Policies not found in role")
	}
	if len(policies) != 2 {
		t.Fatalf("Policies len = %d, want 2 (S3 + SSM output)", len(policies))
	}
	found := false
	for _, p := range policies {
		if pm := p.(map[string]any); pm["PolicyName"] == "fabrica-ssm-output" {
			found = true
		}
	}
	if !found {
		t.Error("fabrica-ssm-output policy missing from DDC role")
	}
	prof, err := InstanceProfileDesiredState(testPlan(BackendZen))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prof), "fabrica-ddc-profile") {
		t.Fatalf("profile: %s", prof)
	}
}

func TestInstanceDesiredState(t *testing.T) {
	raw, err := InstanceDesiredState(testPlan(BackendZen), "sg-1", "dGVzdA==", "fabrica-ddc-profile")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["ImageId"] != "ami-ddc" {
		t.Fatalf("ImageId = %v", doc["ImageId"])
	}
	if doc["IamInstanceProfile"] != "fabrica-ddc-profile" {
		t.Fatalf("IamInstanceProfile = %v, want fabrica-ddc-profile", doc["IamInstanceProfile"])
	}
}

func TestScyllaInstanceDesiredState(t *testing.T) {
	raw, err := ScyllaInstanceDesiredState(testPlan(BackendScylla), "sg-1", "dGVzdA==", "fabrica-ddc-profile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "ami-scylla") {
		t.Fatalf("%s", raw)
	}
}

// TestInstanceDesiredStatesEncryptedRoot pins #513: the DDC coordinator,
// Scylla, and edge instances each request Encrypted=true on the AMI's own
// root device, so the root volume is encrypted even when the AMI snapshot
// is not.
func TestInstanceDesiredStatesEncryptedRoot(t *testing.T) {
	check := func(t *testing.T, raw json.RawMessage, dev string) {
		t.Helper()
		var doc map[string]any
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("invalid desired state: %v", err)
		}
		bdms, ok := doc["BlockDeviceMappings"].([]any)
		if !ok || len(bdms) != 1 {
			t.Fatalf("BlockDeviceMappings = %v, want only the root", doc["BlockDeviceMappings"])
		}
		m0 := bdms[0].(map[string]any)
		if m0["DeviceName"] != dev {
			t.Errorf("DeviceName = %v, want %s", m0["DeviceName"], dev)
		}
		if enc, ok := m0["Ebs"].(map[string]any)["Encrypted"].(bool); !ok || !enc {
			t.Errorf("Ebs.Encrypted = %v, want true", m0["Ebs"])
		}
	}

	t.Run("coordinator", func(t *testing.T) {
		plan := testPlan(BackendZen)
		plan.RootDeviceName = "/dev/xvda1"
		raw, err := InstanceDesiredState(plan, "sg-1", "dGVzdA==", "fabrica-ddc-profile")
		if err != nil {
			t.Fatalf("InstanceDesiredState: %v", err)
		}
		check(t, raw, "/dev/xvda1")
	})

	t.Run("scylla", func(t *testing.T) {
		plan := testPlan(BackendScylla)
		plan.ScyllaRootDeviceName = "/dev/xvdb"
		raw, err := ScyllaInstanceDesiredState(plan, "sg-1", "dGVzdA==", "fabrica-ddc-profile")
		if err != nil {
			t.Fatalf("ScyllaInstanceDesiredState: %v", err)
		}
		check(t, raw, "/dev/xvdb")
	})

	t.Run("edge", func(t *testing.T) {
		plan := testEdgePlan()
		plan.RootDeviceName = "/dev/xvda1"
		raw, err := EdgeInstanceDesiredState(plan, "sg-edge", "dGVzdA==")
		if err != nil {
			t.Fatalf("EdgeInstanceDesiredState: %v", err)
		}
		check(t, raw, "/dev/xvda1")
	})

	t.Run("empty root device refuses", func(t *testing.T) {
		plan := testPlan(BackendZen)
		plan.RootDeviceName = ""
		if _, err := InstanceDesiredState(plan, "sg-1", "dGVzdA==", "fabrica-ddc-profile"); err == nil {
			t.Fatal("expected an error for an empty root device")
		}
	})
}

func testEdgePlan() *EdgePlan {
	return &EdgePlan{
		Account:             "123",
		Region:              "eu-west-1",
		AmiID:               "ami-edge",
		InstanceType:        "m7i.large",
		VolumeSize:          250,
		RootDeviceName:      "/dev/sda1",
		PublicPort:          80,
		InternalPort:        8080,
		AllowedCIDR:         "10.0.0.0/8",
		InternalCIDR:        "10.0.0.0/8",
		VPCID:               "vpc-edge",
		SubnetID:            "subnet-edge",
		Bucket:              "fabrica-ddc-test",
		Namespace:           "deriveddatacache",
		SGName:              "fabrica-ddc-sg-eu-west-1",
		InstanceName:        "fabrica-ddc-edge-eu-west-1",
		InstanceProfileName: "fabrica-ddc-profile",
	}
}

func TestEdgeSGDesiredState(t *testing.T) {
	raw, err := EdgeSGDesiredState(testEdgePlan())
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	if !strings.Contains(s, "public API") || !strings.Contains(s, "8080") {
		t.Fatalf("edge SG: %s", s)
	}
	if strings.Contains(s, "9042") {
		t.Fatalf("edge SG must not open Scylla CQL: %s", s)
	}
	if strings.Contains(s, "fabrica-ddc-sg-eu-west-1") == false {
		t.Fatalf("edge SG missing name: %s", s)
	}
}

func TestEdgeInstanceDesiredState(t *testing.T) {
	raw, err := EdgeInstanceDesiredState(testEdgePlan(), "sg-edge", "dGVzdA==")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["ImageId"] != "ami-edge" {
		t.Fatalf("ImageId = %v", doc["ImageId"])
	}
	if doc["IamInstanceProfile"] != "fabrica-ddc-profile" {
		t.Fatalf("IamInstanceProfile = %v", doc["IamInstanceProfile"])
	}
	if !strings.Contains(string(raw), "fabrica-ddc-edge-eu-west-1") {
		t.Fatalf("instance name missing: %s", raw)
	}
	if !strings.Contains(string(raw), "FabricaRole") {
		t.Fatalf("edge role tag missing: %s", raw)
	}
}
