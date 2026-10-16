// Package iamrole provides shared helpers for building IAM role desired-state
// documents used by multiple modules (perforce, ddc, ci, deploy) when creating
// IAM roles via Cloud Control.
package iamrole

import (
	"encoding/json"
	"fmt"
	"strings"
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

// RoleOption sets an optional Cloud Control property on an IAM role document.
type RoleOption func(*roleOptions)

type roleOptions struct {
	Path                string
	PermissionsBoundary string
}

// WithPath sets the IAM path. The path must start and end with "/".
func WithPath(path string) RoleOption {
	return func(o *roleOptions) { o.Path = path }
}

// WithPermissionsBoundary sets the permissions boundary policy ARN.
func WithPermissionsBoundary(arn string) RoleOption {
	return func(o *roleOptions) { o.PermissionsBoundary = arn }
}

// Options builds the role options for a resolved path and boundary.
// Empty values are omitted so existing documents stay unchanged.
func Options(path, boundary string) []RoleOption {
	var opts []RoleOption
	if path != "" {
		opts = append(opts, WithPath(path))
	}
	if boundary != "" {
		opts = append(opts, WithPermissionsBoundary(boundary))
	}
	return opts
}

// RoleDocument builds the Cloud Control desired-state document for an IAM role.
// It sets RoleName, AssumeRolePolicyDocument, ManagedPolicyArns, Tags, and
// optionally inline Policies, Path, and PermissionsBoundary. Pass nil for
// fields that should be omitted.
func RoleDocument(roleName string, service string, managedArns []string, policies []map[string]any, tags map[string]string, opts ...RoleOption) (json.RawMessage, error) {
	var o roleOptions
	for _, opt := range opts {
		if opt != nil {
			opt(&o)
		}
	}
	if err := validateRolePath(o.Path); err != nil {
		return nil, err
	}
	if err := validateRoleBoundary(o.PermissionsBoundary); err != nil {
		return nil, err
	}
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
	if o.Path != "" {
		doc["Path"] = o.Path
	}
	if o.PermissionsBoundary != "" {
		doc["PermissionsBoundary"] = o.PermissionsBoundary
	}
	doc["Tags"] = RoleTags(roleName, tags)
	return json.Marshal(doc)
}

func validateRolePath(path string) error {
	if path == "" {
		return nil
	}
	if len(path) < 2 || !strings.HasPrefix(path, "/") || !strings.HasSuffix(path, "/") || strings.Contains(path, "//") {
		return fmt.Errorf("iam.path %q must start and end with /", path)
	}
	return nil
}

func validateRoleBoundary(arn string) error {
	if arn == "" {
		return nil
	}
	if !rolePolicyARN(arn) {
		return fmt.Errorf("iam.permissionsBoundary %q must be an IAM policy ARN", arn)
	}
	return nil
}

// rolePolicyARN matches config.iamPolicyARN. The packages stay independent so
// the plan layer does not import config and config does not import iamrole.
func rolePolicyARN(arn string) bool {
	switch {
	case strings.HasPrefix(arn, "arn:aws:iam::"),
		strings.HasPrefix(arn, "arn:aws-us-gov:iam::"),
		strings.HasPrefix(arn, "arn:aws-cn:iam::"):
	default:
		return false
	}
	const marker = ":policy/"
	idx := strings.Index(arn, marker)
	if idx < 0 {
		return false
	}
	name := arn[idx+len(marker):]
	return name != "" && name != "/"
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

// CICodeBuildInlinePolicy builds the least-privilege inline IAM policy the
// CodeBuild role needs: CloudWatch Logs writes scoped to this project's log
// group plus the EC2/VPC actions for ENI lifecycle and coordinator address
// resolution. One document, two renderers (create desired-state + export).
func CICodeBuildInlinePolicy(region, account, projectName string) map[string]any {
	if region == "" {
		region = "*"
	}
	if account == "" {
		account = "*"
	}
	logsARN := fmt.Sprintf("arn:aws:logs:%s:%s:log-group:/aws/codebuild/%s*", region, account, projectName)
	return map[string]any{
		"PolicyName": "fabrica-ci-inline",
		"PolicyDocument": map[string]any{
			"Version": "2012-10-17",
			"Statement": []map[string]any{
				{
					"Effect":   "Allow",
					"Action":   []string{"logs:CreateLogGroup", "logs:CreateLogStream", "logs:PutLogEvents"},
					"Resource": []string{logsARN, logsARN + ":*"},
				},
				{
					"Effect": "Allow",
					"Action": []string{
						"ec2:CreateNetworkInterface",
						"ec2:CreateNetworkInterfacePermission",
						"ec2:DeleteNetworkInterface",
						"ec2:DescribeDhcpOptions",
						"ec2:DescribeInstances",
						"ec2:DescribeNetworkInterfaces",
						"ec2:DescribeSecurityGroups",
						"ec2:DescribeSubnets",
						"ec2:DescribeTags",
						"ec2:DescribeVpcs",
						"ec2:CreateTags",
						"ec2:DeleteTags",
					},
					"Resource": "*",
				},
			},
		},
	}
}

// DeployS3ReadPolicy builds the inline policy the GameLift role needs to
// read a build artifact from S3: s3:GetObject on the bucket's objects.
func DeployS3ReadPolicy(bucket string) map[string]any {
	bucketArn := fmt.Sprintf("arn:aws:s3:::%s/*", bucket)
	return map[string]any{
		"PolicyName": "fabrica-deploy-s3-read",
		"PolicyDocument": map[string]any{
			"Version": "2012-10-17",
			"Statement": []map[string]any{
				{
					"Effect":   "Allow",
					"Action":   []string{"s3:GetObject"},
					"Resource": bucketArn,
				},
			},
		},
	}
}
