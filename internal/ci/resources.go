package ci

import (
	"encoding/json"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

// ProjectSpec builds the provider-agnostic CodeBuild project spec for this plan.
// CodeBuild projects are created via the cloud.CodeBuildRunner SDK path (not
// Cloud Control, which does not support AWS::CodeBuild::Project CREATE).
func ProjectSpec(plan *CreatePlan, roleARN string) cloud.CodeBuildProjectSpec {
	return cloud.CodeBuildProjectSpec{
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
}

// RoleDesiredState returns the Cloud Control desired-state JSON for the IAM role
// CodeBuild assumes. The trust policy allows codebuild.amazonaws.com; a single
// inline policy grants CloudWatch Logs writes (scoped to this project's log
// group) and ec2:DescribeInstances (to resolve coordinator addresses).
func RoleDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	doc := map[string]any{
		"RoleName": plan.RoleName,
		"AssumeRolePolicyDocument": map[string]any{
			"Version": "2012-10-17",
			"Statement": []map[string]any{
				{
					"Effect":    "Allow",
					"Principal": map[string]any{"Service": "codebuild.amazonaws.com"},
					"Action":    "sts:AssumeRole",
				},
			},
		},
		"Policies": []map[string]any{
			{
				"PolicyName":     "fabrica-ci-inline",
				"PolicyDocument": json.RawMessage(inlinePolicyDocument(plan)),
			},
		},
		"Tags": []map[string]string{
			{"Key": "ManagedBy", "Value": "fabrica"},
			{"Key": "Name", "Value": plan.RoleName},
		},
	}
	return json.Marshal(doc)
}
