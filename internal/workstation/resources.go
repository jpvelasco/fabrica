package workstation

import (
	"encoding/json"

	"github.com/jpvelasco/fabrica/internal/ec2state"
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
		nil,
	)
}

// InstanceDesiredState returns the Cloud Control desired-state JSON for the
// workstation EC2 instance. Uses /dev/sda1 as the root EBS device.
func InstanceDesiredState(plan *CreatePlan, sgID, userData string) (json.RawMessage, error) {
	return ec2state.Build(
		[]ec2state.InstanceOption{
			ec2state.WithAMI(plan.AmiID),
			ec2state.WithInstanceType(plan.InstanceType),
			ec2state.WithSubnet(plan.SubnetID),
			ec2state.WithSecurityGroup(sgID),
			ec2state.WithUserData(userData),
			ec2state.WithVolumeSize(plan.VolumeSize),
			ec2state.WithInstanceName(plan.InstanceName),
		},
		ec2state.WithDeviceName("/dev/sda1"),
	)
}
