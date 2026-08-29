package ci

import (
	"encoding/json"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/ec2state"
	"github.com/jpvelasco/fabrica/internal/iamrole"
)

// ProjectSpec builds the provider-agnostic CodeBuild project spec for this plan.
// CodeBuild projects are created via the cloud.CodeBuildRunner SDK path (not
// Cloud Control, which does not support AWS::CodeBuild::Project CREATE).
// sgID is the security group the project's builds join; when empty, the spec
// carries no VPC config and builds run outside any VPC.
func ProjectSpec(plan *CreatePlan, roleARN, sgID string) cloud.CodeBuildProjectSpec {
	spec := cloud.CodeBuildProjectSpec{
		Name:           plan.ProjectName,
		ServiceRoleARN: roleARN,
		ComputeType:    plan.ComputeType,
		Image:          plan.Image,
		BuildTimeout:   plan.BuildTimeout,
		Buildspec:      BuildspecRaw(plan),
		EnvDefaults: map[string]string{
			"HORDE_URL":      plan.HordeURL,
			"FABRICA_REGION": plan.Region,
		},
		Tags: map[string]string{
			"ManagedBy":     "fabrica",
			"FabricaModule": "ci",
			"Name":          plan.ProjectName,
		},
	}
	if plan.VPCID != "" && plan.SubnetID != "" && sgID != "" {
		spec.VpcConfig = &cloud.CodeBuildVpcConfig{
			VpcID:            plan.VPCID,
			SubnetID:         plan.SubnetID,
			SecurityGroupIDs: []string{sgID},
		}
	}
	return spec
}

// SGDesiredState returns the Cloud Control desired-state JSON for the CI
// security group. It lives in the plan's VPC with no inbound rules — the group
// exists so CodeBuild's ENI is routable within the VPC and can egress to the
// Horde coordinator. Egress to the coordinator port is governed by the Horde
// security group's inbound allow (see horde.allowedCidr).
func SGDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	return ec2state.SGDesiredState(
		plan.SGName,
		"Fabrica CI CodeBuild project networking (in-VPC egress to coordinators)",
		plan.VPCID,
		nil,
		map[string]string{"FabricaModule": "ci"},
	)
}

// RoleDesiredState returns the Cloud Control desired-state JSON for the IAM role
// CodeBuild assumes. The trust policy allows codebuild.amazonaws.com; a single
// inline policy grants CloudWatch Logs writes (scoped to this project's log
// group) and ec2:DescribeInstances (to resolve coordinator addresses).
func RoleDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	doc := map[string]any{
		"RoleName":                 plan.RoleName,
		"AssumeRolePolicyDocument": iamrole.AssumeRolePolicyDocument(iamrole.ServiceCodeBuild),
		"Policies": []map[string]any{
			iamrole.CICodeBuildInlinePolicy(plan.Region, plan.Account, plan.ProjectName),
		},
		"Tags": iamrole.RoleTags(plan.RoleName, nil),
	}
	return json.Marshal(doc)
}
