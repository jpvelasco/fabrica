// Package deploy is the pure plan layer for the Fabrica deploy module. It builds
// the setup/promote plans and Cloud Control desired-state JSON for the GameLift
// IAM role, alias, build, and fleet that deploy a game-server build. It imports
// no AWS SDK — the cmd/deploy layer executes plans via rt.Provider.Resources()
// and the cloud.GameLiftManager auxiliary interface.
package deploy

import (
	"fmt"
	"strings"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

const (
	TypeAWSIAMRole    = "AWS::IAM::Role"
	TypeGameLiftAlias = "AWS::GameLift::Alias"
	TypeGameLiftBuild = "AWS::GameLift::Build"
	TypeGameLiftFleet = "AWS::GameLift::Fleet"

	defaultRoleName                 = "fabrica-deploy-gamelift"
	defaultAliasName                = "fabrica-deploy"
	defaultFleetPrefix              = "fabrica-fleet"
	defaultBuildPrefix              = "fabrica-build"
	defaultInstanceType             = "c5.large"
	defaultFleetType                = "ON_DEMAND"
	defaultBuildOS                  = "AMAZON_LINUX_2"
	defaultLaunchPath               = "/local/game/ServerApp"
	defaultFromPort                 = 7777
	defaultToPort                   = 7777
	defaultDesiredInstances         = 1
	defaultActivationTimeoutMinutes = 45
)

// SetupPlan is the resolved deploy setup plan (IAM role + alias).
type SetupPlan struct {
	Account       string
	Region        string
	RoleName      string
	AliasName     string
	BuildBucket   string
	CostResources []cost.Resource
}

// PromotePlan is the resolved promote plan (build registration + new fleet).
type PromotePlan struct {
	Account                  string
	Region                   string
	BuildVersion             string
	RoleName                 string
	RoleARN                  string
	AliasID                  string
	FleetName                string
	BuildName                string
	InstanceType             string
	FleetType                string
	LaunchPath               string
	BuildOS                  string
	S3Bucket                 string
	S3Key                    string
	FromPort                 int
	ToPort                   int
	DesiredInstances         int
	ActivationTimeoutMinutes int
	CostResources            []cost.Resource
}

// NewSetupPlan builds the setup plan, applying defaults for unset config fields.
func NewSetupPlan(cfg config.DeployConfig, account, region string) *SetupPlan {
	roleName := cfg.RoleName
	if roleName == "" {
		roleName = defaultRoleName
	}
	aliasName := cfg.AliasName
	if aliasName == "" {
		aliasName = defaultAliasName
	}
	return &SetupPlan{
		Account:     account,
		Region:      region,
		RoleName:    roleName,
		AliasName:   aliasName,
		BuildBucket: cfg.BuildBucket,
		CostResources: []cost.Resource{
			{TypeName: TypeAWSIAMRole, Name: roleName},
			{TypeName: TypeGameLiftAlias, Name: aliasName},
		},
	}
}

// NewPromotePlan builds the promote plan. s3Bucket/s3Key override the convention
// (cfg.BuildBucket + "builds/<version>/server.zip") when non-empty.
func NewPromotePlan(cfg config.DeployConfig, account, region, buildVersion, roleARN, aliasID, s3Bucket, s3Key string) *PromotePlan {
	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = defaultInstanceType
	}
	fleetType := cfg.FleetType
	if fleetType == "" {
		fleetType = defaultFleetType
	}
	launchPath := cfg.LaunchPath
	if launchPath == "" {
		launchPath = defaultLaunchPath
	}
	buildOS := cfg.BuildOS
	if buildOS == "" {
		buildOS = defaultBuildOS
	}
	fromPort := cfg.FromPort
	if fromPort == 0 {
		fromPort = defaultFromPort
	}
	toPort := cfg.ToPort
	if toPort == 0 {
		toPort = defaultToPort
	}
	desired := cfg.DesiredInstances
	if desired <= 0 {
		desired = defaultDesiredInstances
	}
	timeout := cfg.ActivationTimeoutMinutes
	if timeout <= 0 {
		timeout = defaultActivationTimeoutMinutes
	}
	if s3Bucket == "" {
		s3Bucket = cfg.BuildBucket
	}
	if s3Key == "" {
		s3Key = fmt.Sprintf("builds/%s/server.zip", buildVersion)
	}
	slug := sanitize(buildVersion)
	roleName := cfg.RoleName
	if roleName == "" {
		roleName = defaultRoleName
	}
	return &PromotePlan{
		Account:                  account,
		Region:                   region,
		BuildVersion:             buildVersion,
		RoleName:                 roleName,
		RoleARN:                  roleARN,
		AliasID:                  aliasID,
		FleetName:                fmt.Sprintf("%s-%s", defaultFleetPrefix, slug),
		BuildName:                fmt.Sprintf("%s-%s", defaultBuildPrefix, slug),
		InstanceType:             instanceType,
		FleetType:                fleetType,
		LaunchPath:               launchPath,
		BuildOS:                  buildOS,
		S3Bucket:                 s3Bucket,
		S3Key:                    s3Key,
		FromPort:                 fromPort,
		ToPort:                   toPort,
		DesiredInstances:         desired,
		ActivationTimeoutMinutes: timeout,
		CostResources: []cost.Resource{
			{TypeName: TypeGameLiftFleet, Name: fleetCostName(instanceType, desired)},
			{TypeName: TypeGameLiftBuild, Name: buildVersion},
		},
	}
}

// fleetCostName encodes the instance type and desired count for the cost
// estimator to parse (mirrors the "gp3-<n>GiB" convention in perforce/cost.go).
func fleetCostName(instanceType string, desired int) string {
	return fmt.Sprintf("%sx%d", instanceType, desired)
}

// sanitize lowercases and replaces characters invalid in GameLift names/IDs.
func sanitize(s string) string {
	s = strings.ToLower(s)
	repl := func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			return r
		}
		return '-'
	}
	return strings.Map(repl, s)
}
