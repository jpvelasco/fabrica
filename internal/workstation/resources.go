package workstation

import (
	"encoding/json"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/ec2state"
	"github.com/jpvelasco/fabrica/internal/iamrole"
)

// SGDesiredState returns the Cloud Control desired-state JSON for the workstation
// security group. Allows TCP 8443 (NICE DCV HTTPS) inbound.
func SGDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	return ec2state.SGDesiredState(
		plan.SGName,
		"Fabrica-managed security group for cloud workstation (NICE DCV)",
		plan.VPCID,
		[]ec2state.SGIngressRule{
			{IpProtocol: "tcp", FromPort: plan.DCVPort, ToPort: plan.DCVPort, CidrIp: plan.AllowedCIDR, Description: "NICE DCV HTTPS"},
		},
		map[string]string{"FabricaModule": "workstation"},
	)
}

// InstanceDesiredState returns the Cloud Control desired-state JSON for the
// workstation EC2 instance. Uses /dev/sda1 as the root EBS device, encrypted
// at launch (#507): the flag rides the Cloud Control desired state and the
// direct RunInstances path alike, so the root volume is encrypted even when
// the AMI snapshot is not and account EBS default encryption is off.
func InstanceDesiredState(plan *CreatePlan, sgID, userData string) (json.RawMessage, error) {
	dsOpts := []ec2state.DesiredStateOption{
		ec2state.WithDeviceName("/dev/sda1"),
		ec2state.WithEncryptedVolume(),
		ec2state.WithIAMProfile(plan.InstanceProfileName),
		ec2state.WithExtraTags("FabricaModule", "workstation"),
	}
	if plan.CreateNonce != "" {
		// The per-create nonce narrows the SDK launch path's adoption lookup
		// to this create's own instance and keys the idempotency token on it.
		dsOpts = append(dsOpts, ec2state.WithExtraTags(cloud.CreateNonceTag, plan.CreateNonce))
	}
	return ec2state.Build(
		ec2state.InstanceSpec{
			ImageID:         plan.AmiID,
			InstanceType:    plan.InstanceType,
			SubnetID:        plan.SubnetID,
			SecurityGroupID: sgID,
			UserData:        userData,
			VolumeSize:      plan.VolumeSize,
			InstanceName:    plan.InstanceName,
		},
		dsOpts...,
	)
}

// RoleDesiredState returns Cloud Control desired-state for the workstation EC2
// instance role: SSM managed instance core, the shared SSM output policy, and
// ssm:GetParameter on the per-create DCV session password parameter only. The
// parameter name (plan.SessionPasswordParameter) is set by the create layer
// before the role is built so the role reads exactly one instance's secret.
func RoleDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	parameterARN, err := SessionPasswordParameterARN(plan.Region, plan.Account, plan.SessionPasswordParameter)
	if err != nil {
		return nil, err
	}
	return iamrole.RoleDocument(plan.RoleName, iamrole.ServiceEC2, []string{
		"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore",
	}, []map[string]any{
		iamrole.SSMOutputPolicy(plan.Region, plan.Account),
		iamrole.SSMParameterReadPolicy(SessionPasswordPolicyName, parameterARN),
	}, map[string]string{"FabricaModule": "workstation"},
		iamrole.Options(plan.IAM.Path, plan.IAM.PermissionsBoundary)...)
}

// InstanceProfileDesiredState returns Cloud Control desired-state for the
// instance profile that wraps the workstation EC2 role.
func InstanceProfileDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	return ec2state.InstanceProfileDesiredState(plan.InstanceProfileName, plan.RoleName, ec2state.ProfileOptions(plan.IAM.Path)...)
}
