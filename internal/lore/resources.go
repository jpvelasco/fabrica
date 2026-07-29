package lore

import (
	"encoding/json"

	"github.com/jpvelasco/fabrica/internal/ec2state"
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
		nil,
	)
}

// InstanceDesiredState returns the Cloud Control desired-state JSON for the
// Lore EC2 instance. ImageId is the user-provided AMI ID from LoreConfig.
// DeleteOnTermination is true: the EBS store dies with the instance.
func InstanceDesiredState(plan *CreatePlan, sgID, userData string) (json.RawMessage, error) {
	return ec2state.Build(ec2state.InstanceSpec{
		ImageID:         plan.AmiID,
		InstanceType:    plan.InstanceType,
		SubnetID:        plan.SubnetID,
		SecurityGroupID: sgID,
		UserData:        userData,
		VolumeSize:      plan.VolumeSize,
		InstanceName:    plan.InstanceName,
	})
}
