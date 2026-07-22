// Package ec2state provides a shared Cloud Control desired-state builder for
// EC2 instances. All modules that provision an EC2 instance (perforce, horde,
// lore, ddc, workstation) use this instead of duplicating the map construction.
package ec2state

import (
	"encoding/base64"
	"encoding/json"
)

// InstanceConfig holds the common fields needed to build an EC2 instance's
// Cloud Control desired-state document.
type InstanceConfig struct {
	amiID        string
	instanceType string
	subnetID     string
	sgID         string
	userData     string
	volumeSize   int
	instanceName string
}

// InstanceOption configures the InstanceConfig via the functional options pattern.
type InstanceOption func(*InstanceConfig)

// WithAMI sets the AMI ID. Required.
func WithAMI(id string) InstanceOption {
	return func(c *InstanceConfig) { c.amiID = id }
}

// WithInstanceType sets the instance type. Required.
func WithInstanceType(t string) InstanceOption {
	return func(c *InstanceConfig) { c.instanceType = t }
}

// WithSubnet sets the subnet ID. Required.
func WithSubnet(id string) InstanceOption {
	return func(c *InstanceConfig) { c.subnetID = id }
}

// WithSecurityGroup sets the security group ID. Required.
func WithSecurityGroup(id string) InstanceOption {
	return func(c *InstanceConfig) { c.sgID = id }
}

// WithUserData sets the base64-encoded user data. Required.
func WithUserData(data string) InstanceOption {
	return func(c *InstanceConfig) { c.userData = data }
}

// WithUserDataRaw sets the user data from a raw string (auto base64 encodes).
func WithUserDataRaw(raw string) InstanceOption {
	return func(c *InstanceConfig) { c.userData = base64.StdEncoding.EncodeToString([]byte(raw)) }
}

// WithVolumeSize sets the EBS volume size in GiB. Required.
func WithVolumeSize(size int) InstanceOption {
	return func(c *InstanceConfig) { c.volumeSize = size }
}

// WithInstanceName sets the instance Name tag. Required.
func WithInstanceName(name string) InstanceOption {
	return func(c *InstanceConfig) { c.instanceName = name }
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

// Build generates the Cloud Control desired-state JSON for an EC2 instance.
// It applies the InstanceConfig options first, then runs each DesiredStateOption
// over the document before marshaling.
func Build(opts []InstanceOption, dsOpts ...DesiredStateOption) (json.RawMessage, error) {
	c := &InstanceConfig{}
	for _, o := range opts {
		o(c)
	}

	doc := map[string]any{
		"InstanceType":     c.instanceType,
		"SubnetId":         c.subnetID,
		"SecurityGroupIds": []string{c.sgID},
		"UserData":         c.userData,
		"BlockDeviceMappings": []map[string]any{
			{
				"DeviceName": "/dev/sdf",
				"Ebs": map[string]any{
					"VolumeSize":          c.volumeSize,
					"VolumeType":          "gp3",
					"DeleteOnTermination": true,
				},
			},
		},
		"Tags": []map[string]string{
			{"Key": "ManagedBy", "Value": "fabrica"},
			{"Key": "Name", "Value": c.instanceName},
		},
		"MetadataOptions": map[string]any{
			"HttpTokens": "required",
		},
	}

	for _, o := range dsOpts {
		o(doc)
	}

	// ImageId is optional — only set when non-empty (perforce dry-runs omit it).
	if c.amiID != "" {
		doc["ImageId"] = c.amiID
	}

	return json.Marshal(doc)
}
