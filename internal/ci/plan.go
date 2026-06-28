// Package ci is the pure plan layer for the Fabrica CI module. It builds the
// CreatePlan and Cloud Control desired-state JSON for the CodeBuild project +
// IAM role that orchestrate Horde BuildGraph jobs. It imports no AWS SDK — the
// cmd/ci layer executes plans via rt.Provider.Resources() and the
// cloud.CodeBuildRunner auxiliary interface.
package ci

import (
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

const (
	// TypeAWSCodeBuildProject and TypeAWSIAMRole are the Cloud Control type names
	// for the resources this module provisions.
	TypeAWSCodeBuildProject = "AWS::CodeBuild::Project"
	TypeAWSIAMRole          = "AWS::IAM::Role"

	// Defaults applied when the corresponding CIConfig field is empty.
	defaultProjectName  = "fabrica-ci"
	defaultRoleName     = "fabrica-ci-codebuild"
	defaultComputeType  = "BUILD_GENERAL1_SMALL"
	defaultImage        = "aws/codebuild/amazonlinux2-x86_64-standard:5.0"
	defaultBuildTimeout = 60
)

// CreatePlan is the resolved CI provisioning plan for one account/region.
type CreatePlan struct {
	Account      string
	Region       string
	ProjectName  string
	RoleName     string
	ComputeType  string
	Image        string
	BuildTimeout int

	// HordeURL is the resolved Horde coordinator base URL (e.g.
	// "http://10.0.1.42:5000"). Empty at setup time — only required when a
	// build is triggered — and injected as the HORDE_URL env default so the
	// project is usable as soon as Horde is reachable.
	HordeURL string

	CostResources []cost.Resource
}

// NewCreatePlan builds the CI plan, applying defaults for any unset CIConfig
// field. hordeURL may be empty at setup time; it becomes the project's default
// HORDE_URL environment variable.
func NewCreatePlan(cfg config.CIConfig, account, region, hordeURL string) *CreatePlan {
	projectName := cfg.ProjectName
	if projectName == "" {
		projectName = defaultProjectName
	}
	computeType := cfg.ComputeType
	if computeType == "" {
		computeType = defaultComputeType
	}
	image := cfg.Image
	if image == "" {
		image = defaultImage
	}
	buildTimeout := cfg.BuildTimeout
	if buildTimeout <= 0 {
		buildTimeout = defaultBuildTimeout
	}

	return &CreatePlan{
		Account:      account,
		Region:       region,
		ProjectName:  projectName,
		RoleName:     defaultRoleName,
		ComputeType:  computeType,
		Image:        image,
		BuildTimeout: buildTimeout,
		HordeURL:     hordeURL,
		CostResources: []cost.Resource{
			{TypeName: TypeAWSIAMRole, Name: defaultRoleName},
			{TypeName: TypeAWSCodeBuildProject, Name: projectName + " (" + computeType + ")"},
		},
	}
}
