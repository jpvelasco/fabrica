package horde

import (
	"encoding/json"

	"github.com/jpvelasco/fabrica/internal/ec2state"
)

// SGDesiredState returns the Cloud Control desired-state JSON for the Horde
// security group. Opens ports 5000 (HTTP) and 5002 (gRPC) to AllowedCIDR.
func SGDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	doc := map[string]any{
		"GroupName":        plan.SGName,
		"GroupDescription": "Fabrica-managed security group for Horde coordinator",
		"VpcId":            plan.VPCID,
		"SecurityGroupIngress": []map[string]any{
			{
				"IpProtocol":  "tcp",
				"FromPort":    plan.Port,
				"ToPort":      plan.Port,
				"CidrIp":      plan.AllowedCIDR,
				"Description": "Horde HTTP API + web UI",
			},
			{
				"IpProtocol":  "tcp",
				"FromPort":    plan.GRPCPort,
				"ToPort":      plan.GRPCPort,
				"CidrIp":      plan.AllowedCIDR,
				"Description": "Horde gRPC (agent connections)",
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
// Horde EC2 instance. ImageId is the user-provided AMI ID from HordeConfig.
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
		ec2state.WithDeleteOnTermination(false),
	)
}
