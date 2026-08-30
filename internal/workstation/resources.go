package workstation

import (
	"encoding/json"

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
// workstation EC2 instance. Uses /dev/sda1 as the root EBS device.
func InstanceDesiredState(plan *CreatePlan, sgID, userData string) (json.RawMessage, error) {
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
		ec2state.WithDeviceName("/dev/sda1"),
		ec2state.WithIAMProfile(plan.InstanceProfileName),
		ec2state.WithExtraTags("FabricaModule", "workstation"),
	)
}

// RoleDesiredState returns Cloud Control desired-state for the workstation EC2
// instance role (SSM managed instance core + shared SSM output policy).
func RoleDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	return iamrole.RoleDocument(plan.RoleName, iamrole.ServiceEC2, []string{
		"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore",
	}, []map[string]any{iamrole.SSMOutputPolicy(plan.Region, plan.Account)}, map[string]string{"FabricaModule": "workstation"})
}

// InstanceProfileDesiredState returns Cloud Control desired-state for the
// instance profile that wraps the workstation EC2 role.
func InstanceProfileDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	return ec2state.InstanceProfileDesiredState(plan.InstanceProfileName, plan.RoleName)
}
