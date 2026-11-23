// Package ci is the pure plan layer for the Fabrica CI module. It builds the
// CreatePlan and Cloud Control desired-state JSON for the CodeBuild project +
// IAM role that orchestrate Horde BuildGraph jobs. It imports no AWS SDK — the
// cmd/ci layer executes plans via rt.Provider.Resources() and the
// cloud.CodeBuildRunner auxiliary interface.
package ci

import (
	"context"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/naming"
	"github.com/jpvelasco/fabrica/internal/topology"
)

const (
	// TypeAWSCodeBuildProject and TypeAWSIAMRole are the Cloud Control type names
	// for the resources this module provisions.
	TypeAWSCodeBuildProject = "AWS::CodeBuild::Project"
	TypeAWSIAMRole          = "AWS::IAM::Role"

	// Defaults applied when the corresponding CIConfig field is empty.
	defaultComputeType  = "BUILD_GENERAL1_SMALL"
	defaultImage        = "aws/codebuild/amazonlinux2-x86_64-standard:5.0"
	defaultBuildTimeout = 60
)

// ProjectName returns the CodeBuild project name: ci.projectName when set,
// otherwise "<namePrefix>-ci".
func ProjectName(p naming.Prefix, cfg config.CIConfig) string {
	if cfg.ProjectName != "" {
		return cfg.ProjectName
	}
	return p.Instance("ci")
}

// CreatePlan is the resolved CI provisioning plan for one account/region.
type CreatePlan struct {
	// Prefix names the role, inline policies, and security group (#705).
	Prefix          naming.Prefix
	HordeConnection horde.Connection
	HordeCAPath     string
	Account         string
	Region          string
	ProjectName     string
	RoleName        string
	ComputeType     string
	Image           string
	BuildTimeout    int

	// HordeURL is the resolved Horde coordinator base URL (e.g.
	// "http://10.0.1.42:5000"). Empty at setup time — only required when a
	// build is triggered — and injected as the HORDE_URL env default so the
	// project is usable as soon as Horde is reachable.
	HordeURL string

	// HordeTokenParameter is the Horde coordinator's service-token SSM
	// SecureString, read from Horde's state at setup time. When set, builds
	// receive it as the PARAMETER_STORE variable HORDE_TOKEN and the role may
	// read exactly that parameter. Empty for a coordinator created before
	// service tokens.
	HordeTokenParameter string

	// VPCID and SubnetID place the CodeBuild project inside a VPC so builds
	// can reach a private-IP Horde. SGName is the project's security group.
	VPCID      string
	SubnetID   string
	DefaultVPC bool
	SGName     string

	// IAM is the resolved path and permissions boundary for the CodeBuild role.
	IAM config.IAM

	CostResources []cost.Resource
}

// NewCreatePlan builds the CI plan, applying defaults for any unset CIConfig
// field; prefix names those defaults (#705). hordeURL may be empty at setup
// time; it becomes the project's default HORDE_URL environment variable. VPCResolver is called only when VPCId or
// SubnetId are absent from cfg, so the project can be placed inside a VPC;
// pass nil to skip resolution (explicit VPC values, or tests).
func NewCreatePlan(ctx context.Context, prefix naming.Prefix, cfg config.CIConfig, account, region, hordeURL string, resolver cloud.VPCResolver) (*CreatePlan, error) {
	if err := cfg.IAM.Validate(); err != nil {
		return nil, err
	}
	if err := horde.ValidateTrustPath("ci.hordeCAPath", cfg.HordeCAPath); err != nil {
		return nil, err
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

	vpcID, subnetID, defaultVPC, err := topology.ResolveVPC(ctx, cfg.VPCId, cfg.SubnetId, resolver)
	if err != nil {
		return nil, err
	}

	return &CreatePlan{
		Prefix:        prefix,
		Account:       account,
		Region:        region,
		ProjectName:   ProjectName(prefix, cfg),
		RoleName:      prefix.Role("ci"),
		ComputeType:   computeType,
		Image:         image,
		BuildTimeout:  buildTimeout,
		HordeURL:      hordeURL,
		HordeCAPath:   cfg.HordeCAPath,
		VPCID:         vpcID,
		SubnetID:      subnetID,
		DefaultVPC:    defaultVPC,
		SGName:        prefix.SG("ci"),
		IAM:           cfg.IAM,
		CostResources: CostResources(prefix, cfg),
	}, nil
}
