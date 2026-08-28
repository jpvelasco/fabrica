// Package iamrole provides shared helpers for building IAM role desired-state
// documents used by multiple modules (perforce, ddc, ci, deploy) when creating
// IAM roles via Cloud Control.
package iamrole

import (
	"encoding/json"
	"fmt"
)

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

// RoleDocument builds the Cloud Control desired-state document for an IAM role.
// It sets RoleName, AssumeRolePolicyDocument, ManagedPolicyArns, Tags, and
// optionally inline Policies. Pass nil for fields that should be omitted.
func RoleDocument(roleName string, service string, managedArns []string, policies []map[string]any, tags map[string]string) (json.RawMessage, error) {
	doc := map[string]any{
		"RoleName":                 roleName,
		"AssumeRolePolicyDocument": AssumeRolePolicyDocument(service),
	}
	if len(managedArns) > 0 {
		doc["ManagedPolicyArns"] = managedArns
	}
	if len(policies) > 0 {
		doc["Policies"] = policies
	}
	doc["Tags"] = RoleTags(roleName, tags)
	return json.Marshal(doc)
}

// SSMOutputPolicy builds the inline policy that lets an EC2 instance publish
// SSM command output to the MDS parameter and the /fabrica/ssm/* CloudWatch
// Logs log group — the reliable output-retrieval sink, because the account's
// AmazonSSMManagedInstanceCore is a narrowed variant without ssm:PutParameter
// or logs:*. Scoped to those resources only; no wildcard beyond the
// /fabrica/ssm/* prefix. Attached to every Fabrica-managed instance role that
// uses SSM (perforce, horde, horde agents, lore, ddc).
func SSMOutputPolicy(region, account string) map[string]any {
	if region == "" {
		region = "*"
	}
	if account == "" {
		account = "*"
	}
	return map[string]any{
		"PolicyName": "fabrica-ssm-output",
		"PolicyDocument": map[string]any{
			"Version": "2012-10-17",
			"Statement": []map[string]any{
				{
					"Sid":      "SSMOutputParams",
					"Effect":   "Allow",
					"Action":   []string{"ssm:PutParameter", "ssm:GetParameter", "ssm:DescribeParameters"},
					"Resource": []string{fmt.Sprintf("arn:aws:ssm:%s:%s:parameter/MDS-*", region, account)},
				},
				{
					"Sid":      "CloudWatchLogsGroup",
					"Effect":   "Allow",
					"Action":   []string{"logs:CreateLogGroup"},
					"Resource": []string{fmt.Sprintf("arn:aws:logs:%s:%s:log-group:/fabrica/ssm/*", region, account)},
				},
				{
					"Sid":    "CloudWatchLogsStream",
					"Effect": "Allow",
					"Action": []string{"logs:CreateLogStream", "logs:PutLogEvents"},
					"Resource": []string{
						fmt.Sprintf("arn:aws:logs:%s:%s:log-group:/fabrica/ssm/*", region, account),
						fmt.Sprintf("arn:aws:logs:%s:%s:log-group:/fabrica/ssm/*:*", region, account),
					},
				},
			},
		},
	}
}

// S3BucketPolicy builds an inline policy document that grants S3 access to a
// specific bucket. bucketName is the bucket name; bucketActions are the actions
// allowed on the bucket ARN (e.g. "s3:ListBucket"); objectActions are the
// actions allowed on object ARNs (e.g. "s3:GetObject"). objPrefix is the
// object key prefix (use "*" for all objects). policyName is the name of the
// inline policy.
func S3BucketPolicy(policyName, bucketName string, bucketActions, objectActions []string, objPrefix string) map[string]any {
	bucketArn := "arn:aws:s3:::" + bucketName
	objectsArn := bucketArn + "/" + objPrefix
	return map[string]any{
		"PolicyName": policyName,
		"PolicyDocument": map[string]any{
			"Version": "2012-10-17",
			"Statement": []map[string]any{
				{
					"Effect":   "Allow",
					"Action":   bucketActions,
					"Resource": []string{bucketArn},
				},
				{
					"Effect":   "Allow",
					"Action":   objectActions,
					"Resource": []string{objectsArn},
				},
			},
		},
	}
}
