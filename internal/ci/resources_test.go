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

func TestProjectDesiredState(t *testing.T) {
	raw, err := ProjectDesiredState(testPlan(), "arn:aws:iam::123456789012:role/fabrica-ci-codebuild")
	if err != nil {
		t.Fatalf("ProjectDesiredState: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc["Name"] != defaultProjectName {
		t.Errorf("Name = %v", doc["Name"])
	}
	if doc["ServiceRole"] != "arn:aws:iam::123456789012:role/fabrica-ci-codebuild" {
		t.Errorf("ServiceRole = %v", doc["ServiceRole"])
	}
	env, ok := doc["Environment"].(map[string]any)
	if !ok {
		t.Fatalf("Environment missing or wrong type")
	}
	if env["ComputeType"] != defaultComputeType {
		t.Errorf("ComputeType = %v", env["ComputeType"])
	}
	src, ok := doc["Source"].(map[string]any)
	if !ok || src["Type"] != "NO_SOURCE" {
		t.Errorf("Source = %v, want NO_SOURCE", doc["Source"])
	}
	if _, ok := src["BuildSpec"].(string); !ok {
		t.Errorf("Source.BuildSpec missing")
	}
}
