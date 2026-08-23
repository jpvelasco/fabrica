package horde

import (
	"encoding/json"

	"github.com/jpvelasco/fabrica/internal/ec2state"
	"github.com/jpvelasco/fabrica/internal/iamrole"
)

// SGDesiredState returns the Cloud Control desired-state JSON for the Horde
// security group. Opens ports 5000 (HTTP) and 5002 (gRPC) to AllowedCIDR.
func SGDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	return ec2state.SGDesiredState(
		plan.SGName,
		"Fabrica-managed security group for Horde coordinator",
		plan.VPCID,
		[]ec2state.SGIngressRule{
			{IpProtocol: "tcp", FromPort: plan.Port, ToPort: plan.Port, CidrIp: plan.AllowedCIDR, Description: "Horde HTTP API + web UI"},
			{IpProtocol: "tcp", FromPort: plan.GRPCPort, ToPort: plan.GRPCPort, CidrIp: plan.AllowedCIDR, Description: "Horde gRPC (agent connections)"},
		},
		map[string]string{"FabricaModule": "horde"},
	)
}

// InstanceDesiredState returns the Cloud Control desired-state JSON for the
// Horde EC2 instance. ImageId is the user-provided AMI ID from HordeConfig.
// When instanceProfileName is non-empty, the instance is attached to that IAM
// instance profile (required for SSM access).
func InstanceDesiredState(plan *CreatePlan, sgID, userData, instanceProfileName string) (json.RawMessage, error) {
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
		ec2state.WithDeleteOnTermination(false),
		ec2state.WithExtraTags("FabricaModule", "horde"),
	}
	if instanceProfileName != "" {
		dsOpts = append(dsOpts, ec2state.WithIAMProfile(instanceProfileName))
	}

	return ec2state.Build(spec, dsOpts...)
}

// RoleDesiredState returns Cloud Control desired-state for the Horde EC2
// instance role (SSM managed instance core).
func RoleDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	return iamrole.RoleDocument(plan.RoleName, iamrole.ServiceEC2, []string{
		"arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore",
	}, nil, map[string]string{"FabricaModule": "horde"})
}

// InstanceProfileDesiredState returns Cloud Control desired-state for the
// instance profile that wraps the Horde EC2 role.
func InstanceProfileDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	return ec2state.InstanceProfileDesiredState(plan.InstanceProfileName, plan.RoleName)
}
