package ddc

import (
	"encoding/json"
	"strings"
	"testing"
)

func testPlan(backend string) *SetupPlan {
	return &SetupPlan{
		Account:             "123",
		Region:              "us-east-1",
		Backend:             backend,
		AmiID:               "ami-ddc",
		ScyllaAmiID:         "ami-scylla",
		InstanceType:        "m7i.xlarge",
		VolumeSize:          500,
		ScyllaInstanceType:  "i4i.large",
		ScyllaVolumeSize:    500,
		PublicPort:          80,
		InternalPort:        8080,
		AllowedCIDR:         "10.0.0.0/8",
		InternalCIDR:        "10.0.0.0/8",
		VPCID:               "vpc-1",
		SubnetID:            "subnet-1",
		Bucket:              "fabrica-ddc-test",
		Namespace:           "deriveddatacache",
		SGName:              "fabrica-ddc-sg",
		InstanceName:        "fabrica-ddc",
		ScyllaInstanceName:  "fabrica-ddc-scylla",
		RoleName:            "fabrica-ddc-role",
		InstanceProfileName: "fabrica-ddc-profile",
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
	prof := doc["IamInstanceProfile"].(map[string]any)
	if prof["Name"] != "fabrica-ddc-profile" {
		t.Fatalf("profile = %v", prof)
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
