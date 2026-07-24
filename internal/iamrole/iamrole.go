// Package iamrole provides shared helpers for building IAM role desired-state
// documents used by multiple modules (perforce, ddc, ci, deploy) when creating
// IAM roles via Cloud Control.
package iamrole

// AssumeRolePolicyDocument builds the standard IAM trust policy envelope that
// allows a specific AWS service to assume the role. The service parameter should
// be the full service principal (e.g. "ec2.amazonaws.com",
// "codebuild.amazonaws.com", "gamelift.amazonaws.com").
func AssumeRolePolicyDocument(service string) map[string]any {
	return map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Effect":    "Allow",
				"Principal": map[string]any{"Service": service},
				"Action":    "sts:AssumeRole",
			},
		},
	}
}

// StandardServicePrincipals are common AWS service principals used as trust
// policy targets for Fabrica-managed roles.
const (
	ServiceEC2       = "ec2.amazonaws.com"
	ServiceCodeBuild = "codebuild.amazonaws.com"
	ServiceGameLift  = "gamelift.amazonaws.com"
)

// RoleTags returns the standard tag array for an IAM role, with the ManagedBy
// and Name tags. Additional module-specific tags can be appended by the caller.
func RoleTags(name string, extra map[string]string) []map[string]string {
	tags := []map[string]string{
		{"Key": "ManagedBy", "Value": "fabrica"},
		{"Key": "Name", "Value": name},
	}
	for k, v := range extra {
		tags = append(tags, map[string]string{"Key": k, "Value": v})
	}
	return tags
}
