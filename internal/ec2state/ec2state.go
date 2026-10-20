// Package ec2state provides a shared Cloud Control desired-state builder for
// EC2 instances. All modules that provision an EC2 instance (perforce, horde,
// lore, ddc, workstation) use this instead of duplicating the map construction.
package ec2state

import (
	"encoding/json"
	"fmt"
	"strings"
)

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

// WithEncryptedVolume sets Encrypted=true on the EBS block device mapping, so
// the volume is encrypted (with the account's default EBS KMS key) even when
// the AMI snapshot is unencrypted and account-level EBS default encryption is
// off. EC2 can encrypt a volume created from an unencrypted snapshot at
// launch; it cannot launch an unencrypted volume from an encrypted snapshot,
// so the flag never conflicts with an encrypted AMI.
func WithEncryptedVolume() DesiredStateOption {
	return func(doc map[string]any) {
		if mappings, ok := doc["BlockDeviceMappings"].([]map[string]any); ok && len(mappings) > 0 {
			if ebs, ok := mappings[0]["Ebs"].(map[string]any); ok {
				ebs["Encrypted"] = true
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

// ProfileOption sets an optional property on an instance profile document.
// Instance profiles accept Path. They do not accept a permissions boundary.
type ProfileOption func(map[string]any)

// WithProfilePath sets the instance profile path. The path must start and
// end with "/".
func WithProfilePath(path string) ProfileOption {
	return func(doc map[string]any) {
		if path != "" {
			doc["Path"] = path
		}
	}
}

// ProfileOptions builds profile options for a resolved IAM path.
// An empty path leaves the document unchanged.
func ProfileOptions(path string) []ProfileOption {
	if path == "" {
		return nil
	}
	return []ProfileOption{WithProfilePath(path)}
}

// InstanceProfileDesiredState returns Cloud Control desired-state for an EC2
// instance profile that wraps an IAM role. Used by perforce and ddc modules.
func InstanceProfileDesiredState(profileName, roleName string, opts ...ProfileOption) (json.RawMessage, error) {
	doc := map[string]any{
		"InstanceProfileName": profileName,
		"Roles":               []string{roleName},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(doc)
		}
	}
	if p, _ := doc["Path"].(string); p != "" && (len(p) < 2 || !strings.HasPrefix(p, "/") || !strings.HasSuffix(p, "/") || strings.Contains(p, "//")) {
		return nil, fmt.Errorf("iam.path %q must start and end with /", p)
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
			"HttpTokens":              "required",
			"HttpPutResponseHopLimit": 1,
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
