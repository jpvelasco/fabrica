// Package ec2state provides a shared Cloud Control desired-state builder for
// EC2 instances. All modules that provision an EC2 instance (perforce, horde,
// lore, ddc, workstation) use this instead of duplicating the map construction.
package ec2state

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	fabricatags "github.com/jpvelasco/fabrica/internal/tags"
)

// rootDeviceRE matches the root device names EC2 AMIs use: /dev/sda1 (Ubuntu,
// most Canonical images), /dev/xvda (Amazon Linux), and the other sd/xvd
// forms. Anything else is refused rather than guessed.
var rootDeviceRE = regexp.MustCompile(`^/dev/(sd|xvd)[a-z]{1,2}[0-9]{0,2}$`)

// ValidRootDeviceName reports whether name has the shape of an EC2 root
// device name (/dev/sda1, /dev/xvda, ...).
func ValidRootDeviceName(name string) bool {
	return rootDeviceRE.MatchString(name)
}

// RootDeviceFor returns the device the encrypted root volume must be mapped
// on, from the AMI's own RootDeviceName and RootDeviceType. subject names the
// config key or launch source in error messages (for example "horde.amiId").
// It fails closed: a missing or malformed root device name, or a root that is
// not EBS, is an error, never a fallback to a default device. Mapping a name
// that is not the AMI's root would attach a second, empty encrypted volume
// and boot the real root from the snapshot unencrypted (#507).
func RootDeviceFor(subject, amiID, rootDeviceName, rootDeviceType string) (string, error) {
	if rootDeviceName == "" {
		return "", fmt.Errorf("%s %s reports no root device name, so Fabrica cannot map an encrypted root volume onto it; refusing to launch", subject, amiID)
	}
	if !ValidRootDeviceName(rootDeviceName) {
		return "", fmt.Errorf("%s %s reports root device %q, which is not an EC2 device name Fabrica can map an encrypted root volume onto; refusing to launch", subject, amiID, rootDeviceName)
	}
	if rootDeviceType != "ebs" {
		return "", fmt.Errorf("%s %s has root device type %q; the module needs an EBS-backed AMI so its root volume can be encrypted", subject, amiID, rootDeviceType)
	}
	return rootDeviceName, nil
}

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

// WithEncryptedRoot maps an encrypted root volume onto the AMI's own root
// device. It finds the existing mapping that already carries rootDevice
// (modules set it with WithDeviceName) and sets Encrypted=true there; when no
// mapping carries the device (modules whose mapping is a data volume, such as
// Perforce) it appends a root mapping. DeleteOnTermination is sent
// explicitly: absent parses as false on the SDK launch path. It fails closed
// on an empty or malformed device name and on a matching mapping without an
// Ebs block, so Build never returns a desired state whose root stays
// unencrypted (#507, #513).
func WithEncryptedRoot(rootDevice string) DesiredStateOption {
	return func(doc map[string]any) error {
		if rootDevice == "" {
			return fmt.Errorf("EC2 root device name is empty; refusing to map an encrypted root volume")
		}
		if !ValidRootDeviceName(rootDevice) {
			return fmt.Errorf("EC2 root device %q is not a valid device name; refusing to map an encrypted root volume", rootDevice)
		}
		mappings, _ := doc["BlockDeviceMappings"].([]map[string]any)
		for _, m := range mappings {
			if m == nil || m["DeviceName"] != rootDevice {
				continue
			}
			ebs, ok := m["Ebs"].(map[string]any)
			if !ok || ebs == nil {
				return fmt.Errorf("cannot request an encrypted root volume: EC2 block device mapping %s has no Ebs block", rootDevice)
			}
			ebs["Encrypted"] = true
			return nil
		}
		doc["BlockDeviceMappings"] = append(mappings, map[string]any{
			"DeviceName": rootDevice,
			"Ebs": map[string]any{
				"Encrypted":           true,
				"DeleteOnTermination": true,
			},
		})
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
		{"Key": fabricatags.ManagedByKey, "Value": fabricatags.ManagedByValue},
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
			{"Key": fabricatags.ManagedByKey, "Value": fabricatags.ManagedByValue},
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
