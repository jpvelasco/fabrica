package ci

import (
	"fmt"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

const TypeAWSCodePipeline = "AWS::CodePipeline::Pipeline"

// PipelinePlan is the documented CodePipeline overlay. V1 does not provision
// a pipeline; trigger still starts CodeBuild directly.
type PipelinePlan struct {
	Enabled bool     `json:"enabled"`
	Name    string   `json:"name"`
	Stages  []string `json:"stages"`
	Note    string   `json:"note"`
}

// BuildPipelinePlan documents the optional pipeline-backed CI path.
func BuildPipelinePlan(cfg config.CIConfig) PipelinePlan {
	name := cfg.ProjectName
	if name == "" {
		name = "fabrica-ci"
	}
	return PipelinePlan{
		Enabled: cfg.Pipeline,
		Name:    name + "-pipeline",
		Stages:  []string{"Source", "Build (CodeBuild → Horde)", "Promote (optional)"},
		Note:    "V1 does not create CodePipeline. fabrica ci trigger still starts the CodeBuild project. Enable ci.pipeline to include the standing pipeline cost line and print this plan.",
	}
}

// PipelineCostResources prices a standing CodePipeline when enabled.
func PipelineCostResources(cfg config.CIConfig) []cost.Resource {
	if !cfg.Pipeline {
		return nil
	}
	plan := BuildPipelinePlan(cfg)
	return []cost.Resource{{TypeName: TypeAWSCodePipeline, Name: plan.Name}}
}

type pipelineEstimator struct{}

func (pipelineEstimator) Estimate(r cost.Resource) (cost.Monthly, error) {
	return cost.Monthly{
		Amount:     1.00,
		Confidence: cost.Medium,
		Note:       fmt.Sprintf("%s (CodePipeline, estimate-only until provisioned)", r.Name),
	}, nil
}

func init() {
	cost.Global.Register(TypeAWSCodePipeline, pipelineEstimator{})
}
