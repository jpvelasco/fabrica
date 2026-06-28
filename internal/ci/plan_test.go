package ci

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
)

func TestNewCreatePlanDefaults(t *testing.T) {
	plan := NewCreatePlan(config.CIConfig{}, "123456789012", "us-west-2", "")

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
	plan := NewCreatePlan(cfg, "123456789012", "us-west-2", "http://10.0.1.5:5000")

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
