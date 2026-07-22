package lore

import (
	"encoding/json"

	"github.com/jpvelasco/fabrica/internal/ec2state"
)

// SGDesiredState returns the Cloud Control desired-state JSON for the Lore
// security group. Opens TCP 41337 (gRPC), UDP 41337 (QUIC), and TCP 41339
// (HTTP health) to AllowedCIDR.
func SGDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	doc := map[string]any{
		"GroupName":        plan.SGName,
		"GroupDescription": "Fabrica-managed security group for Lore loreserver",
		"VpcId":            plan.VPCID,
		"SecurityGroupIngress": []map[string]any{
			{
				"IpProtocol":  "tcp",
				"FromPort":    plan.GRPCPort,
				"ToPort":      plan.GRPCPort,
				"CidrIp":      plan.AllowedCIDR,
				"Description": "Lore gRPC",
			},
			{
				"IpProtocol":  "udp",
				"FromPort":    plan.GRPCPort,
				"ToPort":      plan.GRPCPort,
				"CidrIp":      plan.AllowedCIDR,
				"Description": "Lore QUIC",
			},
			{
				"IpProtocol":  "tcp",
				"FromPort":    plan.HTTPPort,
				"ToPort":      plan.HTTPPort,
				"CidrIp":      plan.AllowedCIDR,
				"Description": "Lore HTTP health",
			},
		},
		"Tags": []map[string]string{
			{"Key": "ManagedBy", "Value": "fabrica"},
			{"Key": "Name", "Value": plan.SGName},
		},
	}
	return json.Marshal(doc)
}

// InstanceDesiredState returns the Cloud Control desired-state JSON for the
// Lore EC2 instance. ImageId is the user-provided AMI ID from LoreConfig.
// DeleteOnTermination is true: the EBS store dies with the instance.
func InstanceDesiredState(plan *CreatePlan, sgID, userData string) (json.RawMessage, error) {
	return ec2state.Build([]ec2state.InstanceOption{
		ec2state.WithAMI(plan.AmiID),
		ec2state.WithInstanceType(plan.InstanceType),
		ec2state.WithSubnet(plan.SubnetID),
		ec2state.WithSecurityGroup(sgID),
		ec2state.WithUserData(userData),
		ec2state.WithVolumeSize(plan.VolumeSize),
		ec2state.WithInstanceName(plan.InstanceName),
	})
}
