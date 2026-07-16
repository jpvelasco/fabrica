package ci

import (
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

// codeBuildEstimator estimates the monthly cost of a CodeBuild project. Builds
// are billed per build-minute, so an idle project costs nothing — the figure is
// a rough placeholder for light CI usage and is low-confidence by nature.
type codeBuildEstimator struct{}

func (codeBuildEstimator) Estimate(r cost.Resource) (cost.Monthly, error) {
	// general1.small Linux on-demand: ~$0.005/build-minute. A project at rest
	// costs $0; assume ~200 build-minutes/month for a small studio pipeline.
	return cost.Monthly{
		Amount:     1.00,
		Confidence: cost.Low,
		Note:       "CodeBuild is billed per build-minute; $0 at idle, ~$0.005/min when building",
	}, nil
}

// iamRoleEstimator estimates the cost of an IAM role — always free.
type iamRoleEstimator struct{}

func (iamRoleEstimator) Estimate(r cost.Resource) (cost.Monthly, error) {
	return cost.Monthly{Amount: 0, Confidence: cost.High, Note: "IAM roles are free"}, nil
}

// CostResources returns the cost inputs for the CI module at the given config,
// applying the same defaults as NewCreatePlan.
func CostResources(cfg config.CIConfig) []cost.Resource {
	projectName := cfg.ProjectName
	if projectName == "" {
		projectName = defaultProjectName
	}
	computeType := cfg.ComputeType
	if computeType == "" {
		computeType = defaultComputeType
	}
	return []cost.Resource{
		{TypeName: TypeAWSIAMRole, Name: defaultRoleName},
		{TypeName: TypeAWSCodeBuildProject, Name: projectName + " (" + computeType + ")"},
	}
}

func init() {
	cost.Global.Register(TypeAWSCodeBuildProject, codeBuildEstimator{})
	cost.Global.Register(TypeAWSIAMRole, iamRoleEstimator{})
}
