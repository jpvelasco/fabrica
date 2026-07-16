package ci

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

func TestCostEstimatorsRegistered(t *testing.T) {
	for _, typeName := range []string{TypeAWSCodeBuildProject, TypeAWSIAMRole} {
		if _, err := cost.Global.Get(typeName); err != nil {
			t.Errorf("estimator not registered for %s: %v", typeName, err)
		}
	}
}

func TestIAMRoleIsFree(t *testing.T) {
	m, err := iamRoleEstimator{}.Estimate(cost.Resource{TypeName: TypeAWSIAMRole})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if m.Amount != 0 {
		t.Errorf("IAM role cost = %v, want 0", m.Amount)
	}
}

func TestCodeBuildEstimateIsLowConfidence(t *testing.T) {
	m, err := codeBuildEstimator{}.Estimate(cost.Resource{TypeName: TypeAWSCodeBuildProject})
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	if m.Confidence != cost.Low {
		t.Errorf("confidence = %v, want low", m.Confidence)
	}
}

func TestCostResourcesDefaults(t *testing.T) {
	got := CostResources(config.CIConfig{})
	if len(got) != 2 {
		t.Fatalf("want 2 resources, got %d: %+v", len(got), got)
	}
	if got[0].TypeName != TypeAWSIAMRole || got[0].Name != defaultRoleName {
		t.Errorf("role: got %+v, want %s", got[0], defaultRoleName)
	}
	expectedProjectName := defaultProjectName + " (" + defaultComputeType + ")"
	if got[1].TypeName != TypeAWSCodeBuildProject || got[1].Name != expectedProjectName {
		t.Errorf("project: got %+v, want %s", got[1], expectedProjectName)
	}
}

func TestCostResourcesOverrides(t *testing.T) {
	got := CostResources(config.CIConfig{ProjectName: "my-project", ComputeType: "BUILD_GENERAL1_LARGE"})
	expectedProjectName := "my-project (BUILD_GENERAL1_LARGE)"
	if got[1].Name != expectedProjectName {
		t.Fatalf("project override not applied: got %s, want %s", got[1].Name, expectedProjectName)
	}
}
