// Package ec2state provides a shared Cloud Control desired-state builder for
// EC2 instances. All modules that provision an EC2 instance (perforce, horde,
// lore, ddc, workstation) use this instead of duplicating the map construction.
package ec2state

import "encoding/json"

// InstanceSpec holds the common fields needed to build an EC2 instance's
// Cloud Control desired-state document. UserData must already be base64 encoded.
type InstanceSpec struct {
	ImageID         string
	InstanceType    string
	SubnetID        string
	SecurityGroupID string
	UserData        string
	VolumeSize      int
	InstanceName    string
}

// DesiredStateOption configures the generated desired-state document.
type DesiredStateOption func(map[string]any)

// WithIAMProfile adds an IAM instance profile (name string, not ARN).
func WithIAMProfile(name string) DesiredStateOption {
	return func(doc map[string]any) {
		doc["IamInstanceProfile"] = name
	}
}

// WithDeviceName overrides the default EBS device name (/dev/sdf).
func WithDeviceName(name string) DesiredStateOption {
	return func(doc map[string]any) {
		if mappings, ok := doc["BlockDeviceMappings"].([]map[string]any); ok && len(mappings) > 0 {
			mappings[0]["DeviceName"] = name
		}
	}
}

// WithDeleteOnTermination sets the EBS DeleteOnTermination flag.
func WithDeleteOnTermination(v bool) DesiredStateOption {
	return func(doc map[string]any) {
		if mappings, ok := doc["BlockDeviceMappings"].([]map[string]any); ok && len(mappings) > 0 {
			if ebs, ok := mappings[0]["Ebs"].(map[string]any); ok {
				ebs["DeleteOnTermination"] = v
			}
		}
	}
}

// WithExtraTags appends additional tags to the instance.
func WithExtraTags(key, value string) DesiredStateOption {
	return func(doc map[string]any) {
		if tags, ok := doc["Tags"].([]map[string]string); ok {
			doc["Tags"] = append(tags, map[string]string{"Key": key, "Value": value})
		}
	}
}

// InstanceProfileDesiredState returns Cloud Control desired-state for an EC2
// instance profile that wraps an IAM role. Used by perforce and ddc modules.
func InstanceProfileDesiredState(profileName, roleName string) (json.RawMessage, error) {
	doc := map[string]any{
		"InstanceProfileName": profileName,
		"Roles":               []string{roleName},
	}
	return json.Marshal(doc)
}

// SGIngressRule is one inbound security group rule.
type SGIngressRule struct {
	IpProtocol  string
	FromPort    int
	ToPort      int
	CidrIp      string
	Description string
}

// SGDesiredState returns Cloud Control desired-state for a security group.
// Callers provide the group name, description, VPC, ingress rules, and any
// extra tags (e.g. FabricaModule). Used by all EC2-based modules to avoid
// duplicating the envelope construction.
func SGDesiredState(groupName, description, vpcID string, rules []SGIngressRule, extraTags map[string]string) (json.RawMessage, error) {
	ingress := make([]map[string]any, len(rules))
	for i, r := range rules {
		ingress[i] = map[string]any{
			"IpProtocol":  r.IpProtocol,
			"FromPort":    r.FromPort,
			"ToPort":      r.ToPort,
			"CidrIp":      r.CidrIp,
			"Description": r.Description,
		}
	}
	tags := []map[string]string{
		{"Key": "ManagedBy", "Value": "fabrica"},
		{"Key": "Name", "Value": groupName},
	}
	for k, v := range extraTags {
		tags = append(tags, map[string]string{"Key": k, "Value": v})
	}
	doc := map[string]any{
		"GroupName":            groupName,
		"GroupDescription":     description,
		"VpcId":                vpcID,
		"SecurityGroupIngress": ingress,
		"Tags":                 tags,
	}
	return json.Marshal(doc)
}

// Build generates the Cloud Control desired-state JSON for an EC2 instance,
// then applies any module-specific DesiredStateOption values before marshaling.
func Build(spec InstanceSpec, dsOpts ...DesiredStateOption) (json.RawMessage, error) {
	doc := map[string]any{
		"InstanceType":     spec.InstanceType,
		"SubnetId":         spec.SubnetID,
		"SecurityGroupIds": []string{spec.SecurityGroupID},
		"UserData":         spec.UserData,
		"BlockDeviceMappings": []map[string]any{
			{
				"DeviceName": "/dev/sdf",
				"Ebs": map[string]any{
					"VolumeSize":          spec.VolumeSize,
					"VolumeType":          "gp3",
					"DeleteOnTermination": true,
				},
			},
		},
		"Tags": []map[string]string{
			{"Key": "ManagedBy", "Value": "fabrica"},
			{"Key": "Name", "Value": spec.InstanceName},
		},
		"MetadataOptions": map[string]any{
			"HttpTokens": "required",
		},
	}

	for _, o := range dsOpts {
		o(doc)
	}

	// ImageId is optional — only set when non-empty (perforce dry-runs omit it).
	if spec.ImageID != "" {
		doc["ImageId"] = spec.ImageID
	}

	return json.Marshal(doc)
}
