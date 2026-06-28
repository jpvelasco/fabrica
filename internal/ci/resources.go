package ci

import "encoding/json"

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

// ProjectDesiredState returns the Cloud Control desired-state JSON for the
// CodeBuild project. Uses NO_SOURCE with an inline buildspec (Fabrica drives the
// build via env overrides at trigger time, not from a source repo in V1).
func ProjectDesiredState(plan *CreatePlan, roleARN string) (json.RawMessage, error) {
	doc := map[string]any{
		"Name":             plan.ProjectName,
		"Description":      "Fabrica-managed CI project orchestrating Horde BuildGraph jobs",
		"ServiceRole":      roleARN,
		"TimeoutInMinutes": plan.BuildTimeout,
		"Artifacts": map[string]any{
			"Type": "NO_ARTIFACTS",
		},
		"Environment": map[string]any{
			"Type":        "LINUX_CONTAINER",
			"ComputeType": plan.ComputeType,
			"Image":       plan.Image,
			"EnvironmentVariables": []map[string]any{
				{"Name": "HORDE_URL", "Value": plan.HordeURL, "Type": "PLAINTEXT"},
				{"Name": "FABRICA_REGION", "Value": plan.Region, "Type": "PLAINTEXT"},
			},
		},
		"Source": map[string]any{
			"Type":      "NO_SOURCE",
			"BuildSpec": BuildspecRaw(plan),
		},
		"Tags": []map[string]string{
			{"Key": "ManagedBy", "Value": "fabrica"},
			{"Key": "Name", "Value": plan.ProjectName},
		},
	}
	return json.Marshal(doc)
}
