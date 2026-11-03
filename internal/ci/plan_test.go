package ci

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestNewCreatePlanDefaults(t *testing.T) {
	plan, err := NewCreatePlan(context.Background(), config.CIConfig{}, "123456789012", "us-west-2", "", nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}

	if plan.ProjectName != defaultProjectName {
		t.Errorf("ProjectName = %q, want %q", plan.ProjectName, defaultProjectName)
	}
	if plan.RoleName != defaultRoleName {
		t.Errorf("RoleName = %q, want %q", plan.RoleName, defaultRoleName)
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
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-west-2", "http://10.0.1.5:5000", nil)
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
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-west-2", "", nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	if plan.VPCID != "vpc-abc" || plan.SubnetID != "subnet-xyz" {
		t.Errorf("VPC = %q/%q, want vpc-abc/subnet-xyz", plan.VPCID, plan.SubnetID)
	}
	if plan.SGName != DefaultSGName {
		t.Errorf("SGName = %q, want %q", plan.SGName, DefaultSGName)
	}
}

func TestNewCreatePlanResolvesDefaultVPC(t *testing.T) {
	resolver := &cloud.TestVPCResolver{VPCID: "vpc-default", SubnetID: "subnet-default"}
	plan, err := NewCreatePlan(context.Background(), config.CIConfig{}, "123456789012", "us-west-2", "", resolver)
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
	_, err := NewCreatePlan(context.Background(), config.CIConfig{}, "123456789012", "us-west-2", "", resolver)
	if err == nil {
		t.Fatal("expected resolver error to propagate")
	}
}

func TestNewCreatePlanCarriesIAM(t *testing.T) {
	cfg := config.CIConfig{IAM: config.IAM{
		Path:                "/ci/",
		PermissionsBoundary: "arn:aws:iam::123456789012:policy/fabrica-boundary",
	}}
	plan, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-west-2", "", nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	if plan.IAM.Path != "/ci/" || plan.IAM.PermissionsBoundary != "arn:aws:iam::123456789012:policy/fabrica-boundary" {
		t.Errorf("IAM = %+v, want the path and boundary carried from config", plan.IAM)
	}
}

func TestNewCreatePlanRejectsBadIAM(t *testing.T) {
	cfg := config.CIConfig{IAM: config.IAM{Path: "fabrica"}}
	_, err := NewCreatePlan(context.Background(), cfg, "123456789012", "us-west-2", "", nil)
	if err == nil || !strings.Contains(err.Error(), "must start and end") {
		t.Fatalf("err = %v, want the bad iam.path error", err)
	}
}
