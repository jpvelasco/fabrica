package perforce

import (
	"encoding/json"

	"github.com/jpvelasco/fabrica/internal/ec2state"
)

// SGDesiredState returns the Cloud Control desired-state JSON for the Perforce
// security group. Allows TCP 1666 inbound; no inbound SSH by default.
func SGDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	return ec2state.SGDesiredState(
		plan.SGName,
		"Fabrica-managed security group for Perforce Helix Core",
		plan.VPCID,
		[]ec2state.SGIngressRule{
			{IpProtocol: "tcp", FromPort: 1666, ToPort: 1666, CidrIp: plan.AllowedCIDR, Description: "Perforce p4d"},
		},
		nil,
	)
}

// InstanceDesiredState returns the Cloud Control desired-state JSON for the
// Perforce EC2 instance. When instanceProfileName is non-empty, the instance
// is attached to that IAM instance profile (required for SSM backup/restore).
// When imageID is non-empty, it is injected as ImageId; otherwise the field
// is omitted (useful for dry-runs where the AMI isn't resolved yet).
func InstanceDesiredState(plan *CreatePlan, sgID, userData, instanceProfileName, imageID string) (json.RawMessage, error) {
	opts := []ec2state.InstanceOption{
		ec2state.WithInstanceType(plan.InstanceType),
		ec2state.WithSubnet(plan.SubnetID),
		ec2state.WithSecurityGroup(sgID),
		ec2state.WithUserData(userData),
		ec2state.WithVolumeSize(plan.VolumeSize),
		ec2state.WithInstanceName(plan.InstanceName),
	}
	if imageID != "" {
		opts = append(opts, ec2state.WithAMI(imageID))
	}

	dsOpts := []ec2state.DesiredStateOption{
		ec2state.WithDeleteOnTermination(false),
	}
	if instanceProfileName != "" {
		dsOpts = append(dsOpts, ec2state.WithIAMProfile(instanceProfileName))
	}

	return ec2state.Build(opts, dsOpts...)
}

// RoleDesiredState returns Cloud Control desired-state for the Perforce EC2
// instance role (SSM managed instance core + optional S3 backup export).
func RoleDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	managed := []string{
		"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore",
	}
	doc := map[string]any{
		"RoleName": plan.RoleName,
		"AssumeRolePolicyDocument": map[string]any{
			"Version": "2012-10-17",
			"Statement": []map[string]any{
				{
					"Effect":    "Allow",
					"Principal": map[string]any{"Service": "ec2.amazonaws.com"},
					"Action":    "sts:AssumeRole",
				},
			},
		},
		"ManagedPolicyArns": managed,
		"Tags": []map[string]string{
			{"Key": "ManagedBy", "Value": "fabrica"},
			{"Key": "Name", "Value": plan.RoleName},
		},
	}
	if plan.BackupS3Export && plan.BackupS3Bucket != "" {
		prefix := plan.BackupS3Prefix
		if prefix == "" {
			prefix = DefaultS3Prefix
		}
		doc["Policies"] = []map[string]any{
			{
				"PolicyName": "fabrica-perforce-backup-s3",
				"PolicyDocument": map[string]any{
					"Version": "2012-10-17",
					"Statement": []map[string]any{
						{
							"Effect":   "Allow",
							"Action":   []string{"s3:ListBucket"},
							"Resource": []string{"arn:aws:s3:::" + plan.BackupS3Bucket},
						},
						{
							"Effect":   "Allow",
							"Action":   []string{"s3:PutObject", "s3:GetObject", "s3:DeleteObject"},
							"Resource": []string{"arn:aws:s3:::" + plan.BackupS3Bucket + "/" + prefix + "*"},
						},
					},
				},
			},
		}
	}
	return json.Marshal(doc)
}

// InstanceProfileDesiredState returns Cloud Control desired-state for the
// instance profile that wraps the Perforce EC2 role.
func InstanceProfileDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	return ec2state.InstanceProfileDesiredState(plan.InstanceProfileName, plan.RoleName)
}
