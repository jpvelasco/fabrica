package ci

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
)

func testPlan() *CreatePlan {
	return NewCreatePlan(config.CIConfig{}, "123456789012", "us-west-2", "http://10.0.1.5:5000")
}

func TestRoleDesiredState(t *testing.T) {
	raw, err := RoleDesiredState(testPlan())
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc["RoleName"] != defaultRoleName {
		t.Errorf("RoleName = %v", doc["RoleName"])
	}
	// Trust policy must allow codebuild.amazonaws.com.
	s := string(raw)
	if !strings.Contains(s, "codebuild.amazonaws.com") {
		t.Errorf("trust policy missing codebuild principal: %s", s)
	}
	if !strings.Contains(s, "logs:PutLogEvents") {
		t.Errorf("inline policy missing logs permission: %s", s)
	}
	if !strings.Contains(s, "ec2:DescribeInstances") {
		t.Errorf("inline policy missing ec2 describe: %s", s)
	}
}

func TestProjectSpec(t *testing.T) {
	spec := ProjectSpec(testPlan(), "arn:aws:iam::123456789012:role/fabrica-ci-codebuild")

	if spec.Name != defaultProjectName {
		t.Errorf("Name = %q", spec.Name)
	}
	if spec.ServiceRoleARN != "arn:aws:iam::123456789012:role/fabrica-ci-codebuild" {
		t.Errorf("ServiceRoleARN = %q", spec.ServiceRoleARN)
	}
	if spec.ComputeType != defaultComputeType {
		t.Errorf("ComputeType = %q", spec.ComputeType)
	}
	if spec.EnvDefaults["HORDE_URL"] != "http://10.0.1.5:5000" {
		t.Errorf("HORDE_URL = %q", spec.EnvDefaults["HORDE_URL"])
	}
	if spec.Buildspec == "" {
		t.Error("Buildspec is empty")
	}
	if spec.Tags["ManagedBy"] != "fabrica" {
		t.Errorf("ManagedBy tag = %q", spec.Tags["ManagedBy"])
	}
}
