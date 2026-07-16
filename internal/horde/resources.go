package horde

import "encoding/json"

// SGDesiredState returns the Cloud Control desired-state JSON for the Horde
// security group. Opens ports 5000 (HTTP) and 5002 (gRPC) to AllowedCIDR.
func SGDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	doc := map[string]any{
		"GroupName":   plan.SGName,
		"Description": "Fabrica-managed security group for Horde coordinator",
		"VpcId":       plan.VPCID,
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
	doc := map[string]any{
		"ImageId":          plan.AmiID,
		"InstanceType":     plan.InstanceType,
		"SubnetId":         plan.SubnetID,
		"SecurityGroupIds": []string{sgID},
		"UserData":         userData,
		"BlockDeviceMappings": []map[string]any{
			{
				"DeviceName": "/dev/sdf",
				"Ebs": map[string]any{
					"VolumeSize":          plan.VolumeSize,
					"VolumeType":          "gp3",
					"DeleteOnTermination": false,
				},
			},
		},
		"Tags": []map[string]string{
			{"Key": "ManagedBy", "Value": "fabrica"},
			{"Key": "Name", "Value": plan.InstanceName},
		},
		"MetadataOptions": map[string]any{
			"HttpTokens": "required",
		},
	}
	return json.Marshal(doc)
}
