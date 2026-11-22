package ci

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

func TestNewCreatePlanDefaults(t *testing.T) {
	plan, err := NewCreatePlan(context.Background(), naming.Default, config.CIConfig{}, "123456789012", "us-west-2", "", nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}

	if plan.ProjectName != "fabrica-ci" {
		t.Errorf("ProjectName = %q, want %q", plan.ProjectName, "fabrica-ci")
	}
	if plan.RoleName != "fabrica-ci-codebuild" {
		t.Errorf("RoleName = %q, want %q", plan.RoleName, "fabrica-ci-codebuild")
	}
	if plan.ComputeType != defaultComputeType {
		t.Errorf("ComputeType = %q, want %q", plan.ComputeType, defaultComputeType)
	}
	if plan.Image != defaultImage {
		t.Errorf("Image = %q, want %q", plan.Image, defaultImage)
	}
	if plan.BuildTimeout != defaultBuildTimeout {
		t.Errorf("BuildTimeout = %d, want %d", plan.BuildTimeout, defaultBuildTimeout)
	}
	if len(plan.CostResources) != 2 {
		t.Errorf("CostResources = %d, want 2", len(plan.CostResources))
	}
}

func TestNewCreatePlanOverrides(t *testing.T) {
	cfg := config.CIConfig{
		ProjectName:  "studio-ci",
		ComputeType:  "BUILD_GENERAL1_LARGE",
		Image:        "aws/codebuild/custom:1.0",
		BuildTimeout: 120,
	}
	plan, err := NewCreatePlan(context.Background(), naming.Default, cfg, "123456789012", "us-west-2", "http://10.0.1.5:5000", nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}

	if plan.ProjectName != "studio-ci" {
		t.Errorf("ProjectName = %q", plan.ProjectName)
	}
	if plan.ComputeType != "BUILD_GENERAL1_LARGE" {
		t.Errorf("ComputeType = %q", plan.ComputeType)
	}
	if plan.Image != "aws/codebuild/custom:1.0" {
		t.Errorf("Image = %q", plan.Image)
	}
	if plan.BuildTimeout != 120 {
		t.Errorf("BuildTimeout = %d", plan.BuildTimeout)
	}
	if plan.HordeURL != "http://10.0.1.5:5000" {
		t.Errorf("HordeURL = %q", plan.HordeURL)
	}
}

func TestNewCreatePlanExplicitVPC(t *testing.T) {
	cfg := config.CIConfig{VPCId: "vpc-abc", SubnetId: "subnet-xyz"}
	plan, err := NewCreatePlan(context.Background(), naming.Default, cfg, "123456789012", "us-west-2", "", nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	if plan.VPCID != "vpc-abc" || plan.SubnetID != "subnet-xyz" {
		t.Errorf("VPC = %q/%q, want vpc-abc/subnet-xyz", plan.VPCID, plan.SubnetID)
	}
	if plan.SGName != "fabrica-ci-sg" {
		t.Errorf("SGName = %q, want %q", plan.SGName, "fabrica-ci-sg")
	}
}

func TestNewCreatePlanResolvesDefaultVPC(t *testing.T) {
	resolver := &cloud.TestVPCResolver{VPCID: "vpc-default", SubnetID: "subnet-default"}
	plan, err := NewCreatePlan(context.Background(), naming.Default, config.CIConfig{}, "123456789012", "us-west-2", "", resolver)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	if plan.VPCID != "vpc-default" || plan.SubnetID != "subnet-default" {
		t.Errorf("VPC = %q/%q, want vpc-default/subnet-default", plan.VPCID, plan.SubnetID)
	}
	if resolver.Calls != 1 {
		t.Errorf("resolver.Calls = %d, want 1", resolver.Calls)
	}
}

func TestNewCreatePlanResolverError(t *testing.T) {
	resolver := &cloud.TestVPCResolver{Err: errors.New("no default VPC")}
	_, err := NewCreatePlan(context.Background(), naming.Default, config.CIConfig{}, "123456789012", "us-west-2", "", resolver)
	if err == nil {
		t.Fatal("expected resolver error to propagate")
	}
}

func TestNewCreatePlanCarriesIAM(t *testing.T) {
	cfg := config.CIConfig{IAM: config.IAM{
		Path:                "/ci/",
		PermissionsBoundary: "arn:aws:iam::123456789012:policy/fabrica-boundary",
	}}
	plan, err := NewCreatePlan(context.Background(), naming.Default, cfg, "123456789012", "us-west-2", "", nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	if plan.IAM.Path != "/ci/" || plan.IAM.PermissionsBoundary != "arn:aws:iam::123456789012:policy/fabrica-boundary" {
		t.Errorf("IAM = %+v, want the path and boundary carried from config", plan.IAM)
	}
}

func TestNewCreatePlanRejectsBadIAM(t *testing.T) {
	cfg := config.CIConfig{IAM: config.IAM{Path: "fabrica"}}
	_, err := NewCreatePlan(context.Background(), naming.Default, cfg, "123456789012", "us-west-2", "", nil)
	if err == nil || !strings.Contains(err.Error(), "must start and end") {
		t.Fatalf("err = %v, want the bad iam.path error", err)
	}
}

func TestNamesFollowPrefix(t *testing.T) {
	acme := naming.MustResolve("acme")
	plan, err := NewCreatePlan(context.Background(), acme, config.CIConfig{}, "123456789012", "us-west-2", "", nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	for _, tc := range []struct{ name, got, want string }{
		{"project", plan.ProjectName, "acme-ci"},
		{"role", plan.RoleName, "acme-ci-codebuild"},
		{"sg", plan.SGName, "acme-ci-sg"},
		{"pipeline", BuildPipelinePlan(acme, config.CIConfig{}).Name, "acme-ci-pipeline"},
		{"cost role", CostResources(acme, config.CIConfig{})[0].Name, "acme-ci-codebuild"},
		{"cost project", CostResources(acme, config.CIConfig{})[1].Name, "acme-ci (BUILD_GENERAL1_SMALL)"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
	if got := ProjectName(acme, config.CIConfig{ProjectName: "studio"}); got != "studio" {
		t.Errorf("explicit ProjectName = %q, want studio", got)
	}
}

func TestRoleDesiredStateFollowsPrefix(t *testing.T) {
	acme := naming.MustResolve("acme")
	plan, err := NewCreatePlan(context.Background(), acme, config.CIConfig{}, "123456789012", "us-west-2", "", nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	plan.HordeTokenParameter = "/acme/horde/0123456789abcdef0123456789abcdef/service-token"
	raw, err := RoleDesiredState(plan)
	if err != nil {
		t.Fatalf("RoleDesiredState: %v", err)
	}
	var doc struct {
		RoleName string `json:"RoleName"`
		Policies []struct {
			PolicyName string `json:"PolicyName"`
		} `json:"Policies"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal role: %v", err)
	}
	if doc.RoleName != "acme-ci-codebuild" {
		t.Errorf("RoleName = %q, want acme-ci-codebuild", doc.RoleName)
	}
	want := []string{"acme-ci-inline", "acme-ci-horde-token"}
	if len(doc.Policies) != len(want) {
		t.Fatalf("policies = %+v, want %v", doc.Policies, want)
	}
	for i, p := range doc.Policies {
		if p.PolicyName != want[i] {
			t.Errorf("policy[%d] = %q, want %q", i, p.PolicyName, want[i])
		}
	}
}
