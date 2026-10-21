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

// DesiredStateOption configures the generated desired-state document. An
// option that cannot apply itself returns an error, and Build fails rather
// than marshaling a document that silently lacks the setting.
type DesiredStateOption func(map[string]any) error

// WithIAMProfile adds an IAM instance profile (name string, not ARN).
func WithIAMProfile(name string) DesiredStateOption {
	return func(doc map[string]any) error {
		doc["IamInstanceProfile"] = name
		return nil
	}
}

// firstMapping returns the document's first block device mapping, or an error
// when the document does not have the shape Build produces.
func firstMapping(doc map[string]any) (map[string]any, error) {
	mappings, ok := doc["BlockDeviceMappings"].([]map[string]any)
	if !ok || len(mappings) == 0 || mappings[0] == nil {
		return nil, fmt.Errorf("EC2 desired state has no block device mapping to configure")
	}
	return mappings[0], nil
}

// WithDeviceName overrides the default EBS device name (/dev/sdf). It fails
// when the document has no mapping: a root-device override that silently does
// nothing would leave the mapping on the wrong device (#507).
func WithDeviceName(name string) DesiredStateOption {
	return func(doc map[string]any) error {
		if name == "" {
			return fmt.Errorf("EC2 block device name is empty")
		}
		m, err := firstMapping(doc)
		if err != nil {
			return err
		}
		m["DeviceName"] = name
		return nil
	}
}

// WithDeleteOnTermination sets the EBS DeleteOnTermination flag.
func WithDeleteOnTermination(v bool) DesiredStateOption {
	return func(doc map[string]any) error {
		if mappings, ok := doc["BlockDeviceMappings"].([]map[string]any); ok && len(mappings) > 0 {
			if ebs, ok := mappings[0]["Ebs"].(map[string]any); ok {
				ebs["DeleteOnTermination"] = v
			}
		}
		return nil
	}
}

// WithEncryptedVolume sets Encrypted=true on the EBS block device mapping, so
// the volume is encrypted (with the account's default EBS KMS key) even when
// the AMI snapshot is unencrypted and account-level EBS default encryption is
// off. EC2 can encrypt a volume created from an unencrypted snapshot at
// launch; it cannot launch an unencrypted volume from an encrypted snapshot,
// so the flag never conflicts with an encrypted AMI. It fails when the
// document has no EBS mapping to encrypt, so Build never returns a desired
// state that silently lacks the flag.
func WithEncryptedVolume() DesiredStateOption {
	return func(doc map[string]any) error {
		m, err := firstMapping(doc)
		if err != nil {
			return fmt.Errorf("cannot request an encrypted volume: %w", err)
		}
		ebs, ok := m["Ebs"].(map[string]any)
		if !ok || ebs == nil {
			return fmt.Errorf("cannot request an encrypted volume: EC2 block device mapping has no Ebs block")
		}
		ebs["Encrypted"] = true
		return nil
	}
}

// WithExtraTags appends additional tags to the instance.
func WithExtraTags(key, value string) DesiredStateOption {
	return func(doc map[string]any) error {
		if tags, ok := doc["Tags"].([]map[string]string); ok {
			doc["Tags"] = append(tags, map[string]string{"Key": key, "Value": value})
		}
		return nil
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
		if o == nil {
			continue
		}
		if err := o(doc); err != nil {
			return nil, err
		}
	}

	// ImageId is optional — only set when non-empty (perforce dry-runs omit it).
	if spec.ImageID != "" {
		doc["ImageId"] = spec.ImageID
	}

	return json.Marshal(doc)
}
