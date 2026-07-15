package lore

import "encoding/json"

// SGDesiredState returns the Cloud Control desired-state JSON for the Lore
// security group. Opens TCP 41337 (gRPC), UDP 41337 (QUIC), and TCP 41339
// (HTTP health) to AllowedCIDR.
func SGDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	doc := map[string]any{
		"GroupName":   plan.SGName,
		"Description": "Fabrica-managed security group for Lore loreserver",
		"VpcId":       plan.VPCID,
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
