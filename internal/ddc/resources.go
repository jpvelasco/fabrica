package ddc

import (
	"encoding/json"

	"github.com/jpvelasco/fabrica/internal/ec2state"
	"github.com/jpvelasco/fabrica/internal/iamrole"
)

// SGDesiredState returns Cloud Control desired-state for the DDC security group.
func SGDesiredState(plan *SetupPlan) (json.RawMessage, error) {
	rules := []ec2state.SGIngressRule{
		{IpProtocol: "tcp", FromPort: plan.PublicPort, ToPort: plan.PublicPort, CidrIp: plan.AllowedCIDR, Description: "Unreal Cloud DDC public API"},
		{IpProtocol: "tcp", FromPort: plan.InternalPort, ToPort: plan.InternalPort, CidrIp: plan.InternalCIDR, Description: "Unreal Cloud DDC internal API (future peers; single-region V1)"},
	}
	if plan.Backend == BackendScylla {
		// CQL only from VPC private ranges via InternalCIDR — not the public AllowedCIDR when open.
		rules = append(rules, ec2state.SGIngressRule{
			IpProtocol: "tcp", FromPort: 9042, ToPort: 9042, CidrIp: plan.InternalCIDR, Description: "Scylla CQL (bootstrap node only)",
		})
	}
	return ec2state.SGDesiredState(plan.SGName, "Fabrica-managed security group for Unreal Cloud DDC", plan.VPCID, rules, map[string]string{
		"FabricaModule": "ddc",
	})
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
	return iamrole.RoleDocument(
		plan.RoleName,
		iamrole.ServiceEC2,
		[]string{"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"},
		[]map[string]any{
			iamrole.S3BucketPolicy("fabrica-ddc-s3", plan.Bucket,
				[]string{"s3:ListBucket", "s3:GetBucketLocation"},
				[]string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject"},
				"*",
			),
		},
		map[string]string{"FabricaModule": "ddc"},
	)
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

// EdgeSGDesiredState returns Cloud Control desired-state for an edge region's
// security group. The edge opens the same public API + internal ports as the
// home stack; replication traffic uses the internal port and is restricted to
// internalCidr — operators extend that CIDR to cover both regions if needed.
func EdgeSGDesiredState(plan *EdgePlan) (json.RawMessage, error) {
	rules := []ec2state.SGIngressRule{
		{IpProtocol: "tcp", FromPort: plan.PublicPort, ToPort: plan.PublicPort, CidrIp: plan.AllowedCIDR, Description: "Unreal Cloud DDC public API (edge)"},
		{IpProtocol: "tcp", FromPort: plan.InternalPort, ToPort: plan.InternalPort, CidrIp: plan.InternalCIDR, Description: "Unreal Cloud DDC internal API (edge; peer wiring is operator-managed)"},
	}
	return ec2state.SGDesiredState(plan.SGName, "Fabrica-managed security group for Unreal Cloud DDC edge", plan.VPCID, rules, map[string]string{
		"FabricaModule": "ddc",
		"FabricaRole":   "edge",
	})
}

// EdgeInstanceDesiredState returns desired-state for the edge region's DDC EC2
// instance. It reuses the home instance profile (IAM is global) and the shared
// blob bucket via cloud-init.
func EdgeInstanceDesiredState(plan *EdgePlan, sgID, userData string) (json.RawMessage, error) {
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
		ec2state.WithExtraTags("FabricaModule", "ddc"),
		ec2state.WithExtraTags("FabricaRole", "edge"),
		ec2state.WithIAMProfile(plan.InstanceProfileName),
	}
	return ec2state.Build(spec, dsOpts...)
}

func ec2DesiredState(amiID, instanceType, subnetID, sgID, userData, profileName string, volumeSize int, name string) (json.RawMessage, error) {
	spec := ec2state.InstanceSpec{
		ImageID:         amiID,
		InstanceType:    instanceType,
		SubnetID:        subnetID,
		SecurityGroupID: sgID,
		UserData:        userData,
		VolumeSize:      volumeSize,
		InstanceName:    name,
	}
	dsOpts := []ec2state.DesiredStateOption{
		ec2state.WithExtraTags("FabricaModule", "ddc"),
	}
	if profileName != "" {
		dsOpts = append(dsOpts, ec2state.WithIAMProfile(profileName))
	}
	return ec2state.Build(spec, dsOpts...)
}
