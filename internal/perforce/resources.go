package perforce

import "encoding/json"

// SGDesiredState returns the Cloud Control desired-state JSON for the Perforce
// security group. Allows TCP 1666 inbound; no inbound SSH by default.
func SGDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	doc := map[string]any{
		"GroupName":   plan.SGName,
		"Description": "Fabrica-managed security group for Perforce Helix Core",
		"VpcId":       plan.VPCID,
		"SecurityGroupIngress": []map[string]any{
			{
				"IpProtocol": "tcp",
				"FromPort":   1666,
				"ToPort":     1666,
				"CidrIp":     "0.0.0.0/0",
				"Description": "Perforce p4d",
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
// Perforce EC2 instance.
func InstanceDesiredState(plan *CreatePlan, sgID, userData string) (json.RawMessage, error) {
	doc := map[string]any{
		"InstanceType": plan.InstanceType,
		"SubnetId":     plan.SubnetID,
		"SecurityGroupIds": []string{sgID},
		"UserData": userData,
		"BlockDeviceMappings": []map[string]any{
			{
				"DeviceName": "/dev/sdf",
				"Ebs": map[string]any{
					"VolumeSize": plan.VolumeSize,
					"VolumeType": "gp3",
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
	// ImageId is intentionally omitted here; the caller resolves the latest
	// Ubuntu 22.04 (jammy) AMI for the region and injects it if needed.
	return json.Marshal(doc)
}
