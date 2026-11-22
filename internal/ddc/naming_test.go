package ddc

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

// acme is a non-default prefix that exercises the names DDC derives from it.
var acme = naming.MustResolve("acme")

func TestNewSetupPlanFollowsPrefix(t *testing.T) {
	plan, err := NewSetupPlan(context.Background(), acme, config.DDCConfig{
		AmiID:    "ami-ddc",
		VPCId:    "vpc-1",
		SubnetId: "subnet-1",
	}, "123456789012", "us-west-2", nil)
	if err != nil {
		t.Fatalf("NewSetupPlan: %v", err)
	}
	want := map[string]string{
		"SGName":              "acme-ddc-sg",
		"InstanceName":        "acme-ddc",
		"ScyllaInstanceName":  "acme-ddc-scylla",
		"RoleName":            "acme-ddc-role",
		"InstanceProfileName": "acme-ddc-profile",
		"Bucket":              "acme-ddc-123456789012-us-west-2",
	}
	got := map[string]string{
		"SGName":              plan.SGName,
		"InstanceName":        plan.InstanceName,
		"ScyllaInstanceName":  plan.ScyllaInstanceName,
		"RoleName":            plan.RoleName,
		"InstanceProfileName": plan.InstanceProfileName,
		"Bucket":              plan.Bucket,
	}
	for field, w := range want {
		if got[field] != w {
			t.Errorf("%s = %q, want %q", field, got[field], w)
		}
	}
}

func TestNewEdgePlanFollowsPrefix(t *testing.T) {
	plan, err := NewEdgePlan(context.Background(), acme, config.DDCConfig{AmiID: "ami-ddc"},
		"123456789012", "us-east-1", "eu-west-1",
		EdgeOptions{VPCID: "vpc-eu", SubnetID: "subnet-eu"}, nil)
	if err != nil {
		t.Fatalf("NewEdgePlan: %v", err)
	}
	if plan.SGName != "acme-ddc-sg-eu-west-1" {
		t.Errorf("SGName = %q, want acme-ddc-sg-eu-west-1", plan.SGName)
	}
	if plan.InstanceName != "acme-ddc-edge-eu-west-1" {
		t.Errorf("InstanceName = %q, want acme-ddc-edge-eu-west-1", plan.InstanceName)
	}
	if plan.InstanceProfileName != "acme-ddc-profile" {
		t.Errorf("InstanceProfileName = %q, want acme-ddc-profile", plan.InstanceProfileName)
	}
	if plan.Bucket != "acme-ddc-123456789012-us-east-1" {
		t.Errorf("Bucket = %q, want acme-ddc-123456789012-us-east-1", plan.Bucket)
	}
}

func TestBucketOrDefaultFollowsPrefix(t *testing.T) {
	if got := BucketOrDefault(acme, "", "123456789012", "us-west-2"); got != "acme-ddc-123456789012-us-west-2" {
		t.Errorf("default bucket = %q", got)
	}
	if got := BucketOrDefault(acme, "custom-bucket", "123456789012", "us-west-2"); got != "custom-bucket" {
		t.Errorf("configured bucket = %q, want custom-bucket", got)
	}
}

func TestCostResourcesFollowsPrefix(t *testing.T) {
	got := CostResources(acme, config.DDCConfig{})
	if len(got) == 0 || got[0].TypeName != cloud.TypeAWSS3Bucket || got[0].Name != "acme-ddc" {
		t.Errorf("bucket cost line = %+v, want acme-ddc", got)
	}
}

func TestRoleDesiredStateFollowsPrefix(t *testing.T) {
	plan := testPlan(BackendZen)
	plan.Prefix = acme
	raw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	for _, want := range []string{"acme-ddc-s3", "acme-ssm-output"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("role document lacks %q", want)
		}
	}
	if strings.Contains(string(raw), "fabrica-ddc-s3") {
		t.Errorf("role document still carries the fabrica-ddc-s3 policy name")
	}
}
