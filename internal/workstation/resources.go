package workstation

import "encoding/json"

// SGDesiredState returns the Cloud Control desired-state JSON for the workstation
// security group. Allows TCP 8443 (NICE DCV HTTPS) inbound.
func SGDesiredState(plan *CreatePlan) (json.RawMessage, error) {
	doc := map[string]any{
		"GroupName":        plan.SGName,
		"GroupDescription": "Fabrica-managed security group for cloud workstation (NICE DCV)",
		"VpcId":            plan.VPCID,
		"SecurityGroupIngress": []map[string]any{
			{
				"IpProtocol":  "tcp",
				"FromPort":    plan.DCVPort,
				"ToPort":      plan.DCVPort,
				"CidrIp":      plan.AllowedCIDR,
				"Description": "NICE DCV HTTPS",
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
// workstation EC2 instance.
func InstanceDesiredState(plan *CreatePlan, sgID, userData string) (json.RawMessage, error) {
	doc := map[string]any{
		"ImageId":          plan.AmiID,
		"InstanceType":     plan.InstanceType,
		"SubnetId":         plan.SubnetID,
		"SecurityGroupIds": []string{sgID},
		"UserData":         userData,
		"BlockDeviceMappings": []map[string]any{
			{
				"DeviceName": "/dev/sda1",
				"Ebs": map[string]any{
					"VolumeSize":          plan.VolumeSize,
					"VolumeType":          "gp3",
					"DeleteOnTermination": true,
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
