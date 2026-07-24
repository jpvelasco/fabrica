package ddc

import (
	"encoding/json"
	"fmt"

	"github.com/jpvelasco/fabrica/internal/ec2state"
)

// SGDesiredState returns Cloud Control desired-state for the DDC security group.
func SGDesiredState(plan *SetupPlan) (json.RawMessage, error) {
	ingress := []map[string]any{
		{
			"IpProtocol":  "tcp",
			"FromPort":    plan.PublicPort,
			"ToPort":      plan.PublicPort,
			"CidrIp":      plan.AllowedCIDR,
			"Description": "Unreal Cloud DDC public API",
		},
		{
			"IpProtocol":  "tcp",
			"FromPort":    plan.InternalPort,
			"ToPort":      plan.InternalPort,
			"CidrIp":      plan.InternalCIDR,
			"Description": "Unreal Cloud DDC internal API (future peers; single-region V1)",
		},
	}
	if plan.Backend == BackendScylla {
		// CQL only from VPC private ranges via InternalCIDR — not the public AllowedCIDR when open.
		ingress = append(ingress, map[string]any{
			"IpProtocol":  "tcp",
			"FromPort":    9042,
			"ToPort":      9042,
			"CidrIp":      plan.InternalCIDR,
			"Description": "Scylla CQL (bootstrap node only)",
		})
	}
	doc := map[string]any{
		"GroupName":            plan.SGName,
		"GroupDescription":     "Fabrica-managed security group for Unreal Cloud DDC",
		"VpcId":                plan.VPCID,
		"SecurityGroupIngress": ingress,
		"Tags": []map[string]string{
			{"Key": "ManagedBy", "Value": "fabrica"},
			{"Key": "Name", "Value": plan.SGName},
			{"Key": "FabricaModule", "Value": "ddc"},
		},
	}
	return json.Marshal(doc)
}

// BucketDesiredState returns Cloud Control desired-state for the DDC blob bucket.
func BucketDesiredState(plan *SetupPlan) (json.RawMessage, error) {
	doc := map[string]any{
		"BucketName": plan.Bucket,
		"PublicAccessBlockConfiguration": map[string]any{
			"BlockPublicAcls":       true,
			"BlockPublicPolicy":     true,
			"IgnorePublicAcls":      true,
			"RestrictPublicBuckets": true,
		},
		"BucketEncryption": map[string]any{
			"ServerSideEncryptionConfiguration": []map[string]any{
				{"ServerSideEncryptionByDefault": map[string]any{"SSEAlgorithm": "AES256"}},
			},
		},
		"VersioningConfiguration": map[string]any{"Status": "Enabled"},
		"Tags": []map[string]string{
			{"Key": "ManagedBy", "Value": "fabrica"},
			{"Key": "Name", "Value": plan.Bucket},
			{"Key": "FabricaModule", "Value": "ddc"},
		},
	}
	return json.Marshal(doc)
}

// RoleDesiredState returns the EC2 instance role (S3 RW on DDC bucket + SSM core).
func RoleDesiredState(plan *SetupPlan) (json.RawMessage, error) {
	bucketArn := fmt.Sprintf("arn:aws:s3:::%s", plan.Bucket)
	objectsArn := bucketArn + "/*"
	doc := map[string]any{
		"RoleName": plan.RoleName,
		"AssumeRolePolicyDocument": map[string]any{
			"Version": "2012-10-17",
			"Statement": []map[string]any{{
				"Effect":    "Allow",
				"Principal": map[string]any{"Service": "ec2.amazonaws.com"},
				"Action":    "sts:AssumeRole",
			}},
		},
		"ManagedPolicyArns": []string{
			"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore",
		},
		"Policies": []map[string]any{{
			"PolicyName": "fabrica-ddc-s3",
			"PolicyDocument": map[string]any{
				"Version": "2012-10-17",
				"Statement": []map[string]any{
					{
						"Effect":   "Allow",
						"Action":   []string{"s3:ListBucket", "s3:GetBucketLocation"},
						"Resource": []string{bucketArn},
					},
					{
						"Effect":   "Allow",
						"Action":   []string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject"},
						"Resource": []string{objectsArn},
					},
				},
			},
		}},
		"Tags": []map[string]string{
			{"Key": "ManagedBy", "Value": "fabrica"},
			{"Key": "Name", "Value": plan.RoleName},
			{"Key": "FabricaModule", "Value": "ddc"},
		},
	}
	return json.Marshal(doc)
}

// InstanceProfileDesiredState wraps the DDC role for EC2 attachment.
func InstanceProfileDesiredState(plan *SetupPlan) (json.RawMessage, error) {
	return ec2state.InstanceProfileDesiredState(plan.InstanceProfileName, plan.RoleName)
}

// InstanceDesiredState returns Cloud Control desired-state for the DDC (Jupiter) EC2 instance.
func InstanceDesiredState(plan *SetupPlan, sgID, userData, instanceProfileName string) (json.RawMessage, error) {
	return ec2DesiredState(
		plan.AmiID, plan.InstanceType, plan.SubnetID, sgID, userData, instanceProfileName,
		plan.VolumeSize, plan.InstanceName,
	)
}

// ScyllaInstanceDesiredState returns desired-state for the optional 1-node Scylla EC2.
func ScyllaInstanceDesiredState(plan *SetupPlan, sgID, userData, instanceProfileName string) (json.RawMessage, error) {
	return ec2DesiredState(
		plan.ScyllaAmiID, plan.ScyllaInstanceType, plan.SubnetID, sgID, userData, instanceProfileName,
		plan.ScyllaVolumeSize, plan.ScyllaInstanceName,
	)
}

func ec2DesiredState(amiID, instanceType, subnetID, sgID, userData, profileName string, volumeSize int, name string) (json.RawMessage, error) {
	opts := []ec2state.InstanceOption{
		ec2state.WithAMI(amiID),
		ec2state.WithInstanceType(instanceType),
		ec2state.WithSubnet(subnetID),
		ec2state.WithSecurityGroup(sgID),
		ec2state.WithUserData(userData),
		ec2state.WithVolumeSize(volumeSize),
		ec2state.WithInstanceName(name),
	}
	dsOpts := []ec2state.DesiredStateOption{
		ec2state.WithExtraTags("FabricaModule", "ddc"),
	}
	if profileName != "" {
		dsOpts = append(dsOpts, ec2state.WithIAMProfile(profileName))
	}
	return ec2state.Build(opts, dsOpts...)
}
