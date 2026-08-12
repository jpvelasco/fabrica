package lore

import (
	"encoding/json"

	"github.com/jpvelasco/fabrica/internal/ec2state"
	"github.com/jpvelasco/fabrica/internal/iamrole"
)

// SGDesiredState returns the Cloud Control desired-state JSON for the Lore
// security group. Opens TCP 41337 (gRPC), UDP 41337 (QUIC), and TCP 41339
// (HTTP health) to AllowedCIDR.
func SGDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	return ec2state.SGDesiredState(
		plan.SGName,
		"Fabrica-managed security group for Lore loreserver",
		plan.VPCID,
		[]ec2state.SGIngressRule{
			{IpProtocol: "tcp", FromPort: plan.GRPCPort, ToPort: plan.GRPCPort, CidrIp: plan.AllowedCIDR, Description: "Lore gRPC"},
			{IpProtocol: "udp", FromPort: plan.GRPCPort, ToPort: plan.GRPCPort, CidrIp: plan.AllowedCIDR, Description: "Lore QUIC"},
			{IpProtocol: "tcp", FromPort: plan.HTTPPort, ToPort: plan.HTTPPort, CidrIp: plan.AllowedCIDR, Description: "Lore HTTP health"},
		},
		map[string]string{"FabricaModule": "lore"},
	)
}

// InstanceDesiredState returns the Cloud Control desired-state JSON for the
// Lore EC2 instance. ImageId is the user-provided AMI ID from LoreConfig.
// DeleteOnTermination is true: the EBS store dies with the instance.
// When S3 store is enabled, the instance profile is attached for S3 access.
func InstanceDesiredState(plan *CreatePlan, sgID, userData string) (json.RawMessage, error) {
	spec := ec2state.InstanceSpec{
		ImageID:         plan.AmiID,
		InstanceType:    plan.InstanceType,
		SubnetID:        plan.SubnetID,
		SecurityGroupID: sgID,
		UserData:        userData,
		VolumeSize:      plan.VolumeSize,
		InstanceName:    plan.InstanceName,
	}
	dsOpts := []ec2state.DesiredStateOption{
		ec2state.WithExtraTags("FabricaModule", "lore"),
	}
	if plan.StoreBackend == StoreBackendS3 {
		dsOpts = append(dsOpts, ec2state.WithIAMProfile(plan.InstanceProfileName))
	}
	return ec2state.Build(spec, dsOpts...)
}

// BucketDesiredState returns Cloud Control desired-state for the Lore S3 store
// bucket. Only used when StoreBackend is "s3".
func BucketDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	doc := map[string]any{
		"BucketName": plan.StoreBucket,
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
			{"Key": "Name", "Value": plan.StoreBucket},
			{"Key": "FabricaModule", "Value": "lore"},
		},
	}
	return json.Marshal(doc)
}

// RoleDesiredState returns the EC2 instance role for S3 access on the Lore
// store bucket + SSM core. Only used when StoreBackend is "s3".
func RoleDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	return iamrole.RoleDocument(
		plan.RoleName,
		iamrole.ServiceEC2,
		[]string{"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"},
		[]map[string]any{
			iamrole.S3BucketPolicy("fabrica-lore-store-s3", plan.StoreBucket,
				[]string{"s3:ListBucket", "s3:GetBucketLocation"},
				[]string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject"},
				"*",
			),
		},
		map[string]string{"FabricaModule": "lore"},
	)
}

// InstanceProfileDesiredState wraps the Lore role for EC2 attachment.
// Only used when StoreBackend is "s3".
func InstanceProfileDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	return ec2state.InstanceProfileDesiredState(plan.InstanceProfileName, plan.RoleName)
}
