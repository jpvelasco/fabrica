package ci

import (
	"testing"

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
