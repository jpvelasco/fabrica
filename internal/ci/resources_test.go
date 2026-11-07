package ci

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
)

func testPlan() *CreatePlan {
	plan, err := NewCreatePlan(context.Background(), config.CIConfig{}, "123456789012", "us-west-2", "http://10.0.1.5:5000", nil)
	if err != nil {
		panic(err)
	}
	return plan
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
	if _, ok := doc["Path"]; ok {
		t.Errorf("Path = %v, want absent when no IAM constraints are set", doc["Path"])
	}
	if _, ok := doc["PermissionsBoundary"]; ok {
		t.Errorf("PermissionsBoundary = %v, want absent when no IAM constraints are set", doc["PermissionsBoundary"])
	}
}

// TestRoleDesiredStateWithIAMGuardrails pins #557: the resolved IAM path and
// permissions boundary land on the CodeBuild role document.
func TestRoleDesiredStateWithIAMGuardrails(t *testing.T) {
	plan := testPlan()
	plan.IAM = config.IAM{
		Path:                "/ci/",
		PermissionsBoundary: "arn:aws:iam::123456789012:policy/fabrica-boundary",
	}
	raw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc["Path"] != "/ci/" {
		t.Errorf("Path = %v, want /ci/", doc["Path"])
	}
	if doc["PermissionsBoundary"] != "arn:aws:iam::123456789012:policy/fabrica-boundary" {
		t.Errorf("PermissionsBoundary = %v, want the boundary ARN", doc["PermissionsBoundary"])
	}
}

func TestProjectSpec(t *testing.T) {
	spec := ProjectSpec(testPlan(), "arn:aws:iam::123456789012:role/fabrica-ci-codebuild", "")

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
	// No VPC resolved → spec carries no VpcConfig (builds run VPC-less).
	if spec.VpcConfig != nil {
		t.Errorf("VpcConfig = %+v, want nil when no VPC resolved", spec.VpcConfig)
	}
}

func TestProjectSpecWithVPC(t *testing.T) {
	plan, err := NewCreatePlan(context.Background(), config.CIConfig{VPCId: "vpc-abc", SubnetId: "subnet-xyz"},
		"123456789012", "us-west-2", "", nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	spec := ProjectSpec(plan, "arn:aws:iam::123456789012:role/fabrica-ci-codebuild", "sg-123")

	if spec.VpcConfig == nil {
		t.Fatal("VpcConfig = nil, want set when VPC resolved")
		return
	}
	if spec.VpcConfig.VpcID != "vpc-abc" || spec.VpcConfig.SubnetID != "subnet-xyz" {
		t.Errorf("VpcConfig = %+v, want vpc-abc/subnet-xyz", spec.VpcConfig)
	}
	if len(spec.VpcConfig.SecurityGroupIDs) != 1 || spec.VpcConfig.SecurityGroupIDs[0] != "sg-123" {
		t.Errorf("SecurityGroupIDs = %v, want [sg-123]", spec.VpcConfig.SecurityGroupIDs)
	}
}

func TestProjectSpecWithVPCMissingSG(t *testing.T) {
	plan, err := NewCreatePlan(context.Background(), config.CIConfig{VPCId: "vpc-abc", SubnetId: "subnet-xyz"},
		"123456789012", "us-west-2", "", nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	spec := ProjectSpec(plan, "arn:aws:iam::123456789012:role/fabrica-ci-codebuild", "")
	if spec.VpcConfig != nil {
		t.Errorf("VpcConfig = %+v, want nil when SG ID is empty", spec.VpcConfig)
	}
}

func TestSGDesiredState(t *testing.T) {
	raw, err := SGDesiredState(testPlan())
	if err != nil {
		t.Fatalf("SGDesiredState: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if doc["GroupName"] != DefaultSGName {
		t.Errorf("GroupName = %v, want %q", doc["GroupName"], DefaultSGName)
	}
	if _, ok := doc["SecurityGroupIngress"]; !ok {
		t.Errorf("SecurityGroupIngress missing from desired state: %s", raw)
	}
}

const testHordeTokenParam = "/fabrica/horde/0123456789abcdef0123456789abcdef/service-token" // #nosec G101 -- SSM parameter name fixture, not a credential

func TestProjectSpecPassesHordeTokenAsParameterStore(t *testing.T) {
	plan := testPlan()
	plan.HordeTokenParameter = testHordeTokenParam
	spec := ProjectSpec(plan, "arn:aws:iam::123456789012:role/r", "")
	if spec.SecretEnv["HORDE_TOKEN"] != testHordeTokenParam {
		t.Errorf("SecretEnv = %v", spec.SecretEnv)
	}
	if _, plain := spec.EnvDefaults["HORDE_TOKEN"]; plain {
		t.Error("HORDE_TOKEN must not be a plaintext variable")
	}
}

func TestRoleDesiredStateReadsOnlyTheHordeToken(t *testing.T) {
	plan := testPlan()
	plan.HordeTokenParameter = testHordeTokenParam
	ds, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	got := string(ds)
	if !strings.Contains(got, "ssm:GetParameters") || !strings.Contains(got, "parameter"+testHordeTokenParam) || !strings.Contains(got, HordeTokenPolicyName) {
		t.Errorf("role lacks the scoped token read: %s", got)
	}
	plan.HordeTokenParameter = ""
	ds, err = RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	if strings.Contains(string(ds), "ssm:GetParameters") {
		t.Error("no token parameter, but the role can read SSM parameters")
	}
}

func TestRoleDesiredStateRejectsMalformedHordeTokenParameter(t *testing.T) {
	plan := testPlan()
	plan.HordeTokenParameter = "/fabrica/horde/not-a-per-create-name/service-token" // #nosec G101 -- SSM parameter name fixture, not a credential
	if _, err := RoleDesiredState(plan); err == nil || !strings.Contains(err.Error(), "not a per-create") {
		t.Fatalf("err = %v", err)
	}
}
