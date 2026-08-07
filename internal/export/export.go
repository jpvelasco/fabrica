// Package export provides infrastructure-as-code generation from Fabrica's
// recorded state and configuration. It converts Fabrica's internal plan and
// state data into CloudFormation YAML and Terraform HCL formats.
//
// V1 scope: Horde, Perforce, Lore modules plus the state backend (S3 bucket +
// DynamoDB table). DDC, Workstation, CI, and Deploy are deferred to V2.
//
// The package is pure generation logic with no AWS SDK imports. It reads from
// state.State and config.Config, applies redaction for credential-like fields,
// and produces format-specific output via the Generator interface.
package export

import (
	"fmt"
	"strings"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/state"
)

// Format is the target IaC format for export.
type Format string

const (
	// CloudFormation produces a CloudFormation YAML template.
	CloudFormation Format = "cloudformation"
	// Terraform produces Terraform HCL configuration.
	Terraform Format = "terraform"
)

// Valid returns true if the format is a supported export format.
func (f Format) Valid() bool {
	return f == CloudFormation || f == Terraform
}

// ValidFormat returns true if the format string is a supported export format.
func ValidFormat(s string) bool {
	return Format(s).Valid()
}

// Generator produces IaC output from export modules.
type Generator interface {
	// Generate renders the given modules as the target format.
	Generate(modules []ExportModule) ([]byte, error)
}

// ExportModule is one module's worth of resources for export.
type ExportModule struct {
	Name      string
	Status    string
	Resources []ExportResource
}

// ExportResource is a single resource to be exported.
type ExportResource struct {
	TypeName   string
	LogicalID  string
	Properties map[string]any
	Module     string
}

// NewGenerator returns a Generator for the given format.
func NewGenerator(format Format) (Generator, error) {
	switch format {
	case CloudFormation:
		return &cloudFormationGenerator{}, nil
	case Terraform:
		return &terraformGenerator{}, nil
	default:
		return nil, fmt.Errorf("unsupported export format %q — supported formats: cloudformation, terraform", format)
	}
}

// GenerateOutput reads state and config, builds export modules, and generates
// IaC output in the requested format.
func GenerateOutput(format Format, st *state.State, cfg *config.Config) ([]byte, error) {
	if st == nil {
		return nil, fmt.Errorf("no state available")
	}

	modules, err := buildModules(st, cfg)
	if err != nil {
		return nil, err
	}

	if len(modules) == 0 {
		return nil, fmt.Errorf("no modules to export — no provisioned modules found in state")
	}

	gen, err := NewGenerator(format)
	if err != nil {
		return nil, err
	}

	return gen.Generate(modules)
}

// buildModules constructs export modules from state and config.
func buildModules(st *state.State, cfg *config.Config) ([]ExportModule, error) {
	var modules []ExportModule

	// State backend resources (always exported if account/region known).
	if st.Account != "" || st.Region != "" {
		sb, err := buildStateBackendModule(st, cfg)
		if err != nil {
			return nil, fmt.Errorf("building state backend export: %w", err)
		}
		if sb != nil {
			modules = append(modules, *sb)
		}
	}

	// Module-specific exports.
	for _, ms := range st.Modules {
		var mod ExportModule
		switch ms.Name {
		case "horde":
			mod = buildHordeModule(ms, cfg)
		case "perforce":
			mod = buildPerforceModule(ms, cfg)
		case "lore":
			mod = buildLoreModule(ms, cfg)
		default:
			// DDC, workstation, CI, deploy — not yet supported in V1.
			continue
		}
		if len(mod.Resources) > 0 {
			modules = append(modules, mod)
		}
	}

	return modules, nil
}

// sanitize strips credential-like fields from resource properties.
// UserData base64 blobs are replaced with a redacted placeholder.
func sanitize(props map[string]any) map[string]any {
	out := make(map[string]any, len(props))
	for k, v := range props {
		switch k {
		case "UserData":
			out[k] = "# REDACTED — cloud-init script (not included in export)"
		case "PasswordData":
			out[k] = "# REDACTED"
		default:
			if s, ok := v.(string); ok && looksLikeBase64Blob(s) {
				out[k] = "# REDACTED — credential-like field"
			} else {
				out[k] = v
			}
		}
	}
	return out
}

// looksLikeBase64Blob returns true if the string looks like a base64-encoded blob
// that might contain secrets (long, no whitespace, base64 charset).
func looksLikeBase64Blob(s string) bool {
	if len(s) < 200 {
		return false
	}
	if strings.ContainsAny(s, " \t\n\r") {
		return false
	}
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') &&
			(r < '0' || r > '9') && r != '+' && r != '/' && r != '=' {
			return false
		}
	}
	return true
}

// toLogicalID converts a resource identifier to a valid CloudFormation logical ID
// (alphanumeric, no spaces or special chars).
func toLogicalID(module, typeName, identifier string) string {
	id := strings.ReplaceAll(identifier, "-", "")
	id = strings.ReplaceAll(id, "_", "")
	id = strings.ReplaceAll(id, ".", "")

	if len(id) == 0 {
		id = "X"
	}

	if len(id) > 16 {
		id = id[:16]
	}

	return fmt.Sprintf("%s%s%s",
		strings.ToTitle(module),
		typeNameShort(typeName),
		strings.ToUpper(id[:1]))
}

// typeNameShort returns a short form of a CloudFormation type name for logical IDs.
func typeNameShort(typeName string) string {
	parts := strings.Split(typeName, "::")
	if len(parts) >= 3 {
		return parts[2]
	}
	return parts[len(parts)-1]
}

// buildStateBackendModule generates export resources for the state backend.
func buildStateBackendModule(st *state.State, cfg *config.Config) (*ExportModule, error) {
	names := state.ResolveBackendNames(cfg, st.Account)

	mod := &ExportModule{
		Name:   "state-backend",
		Status: "provisioned",
		Resources: []ExportResource{
			{
				TypeName:  "AWS::S3::Bucket",
				LogicalID: "FabricaStateBucket",
				Properties: map[string]any{
					"BucketName": names.Bucket,
					"VersioningConfiguration": map[string]any{
						"Status": "Enabled",
					},
					"BucketEncryption": map[string]any{
						"ServerSideEncryptionConfiguration": []map[string]any{
							{
								"ServerSideEncryptionByDefault": map[string]any{
									"SSEAlgorithm": "AES256",
								},
							},
						},
					},
					"PublicAccessBlockConfiguration": map[string]any{
						"BlockPublicAcls":       true,
						"BlockPublicPolicy":     true,
						"IgnorePublicAcls":      true,
						"RestrictPublicBuckets": true,
					},
					"Tags": []map[string]string{
						{"Key": "ManagedBy", "Value": "fabrica"},
						{"Key": "Name", "Value": names.Bucket},
					},
				},
			},
			{
				TypeName:  "AWS::DynamoDB::Table",
				LogicalID: "FabricaStateLockTable",
				Properties: map[string]any{
					"TableName": names.Table,
					"KeySchema": []map[string]any{
						{
							"AttributeName": "LockID",
							"KeyType":       "HASH",
						},
					},
					"AttributeDefinitions": []map[string]any{
						{
							"AttributeName": "LockID",
							"AttributeType": "S",
						},
					},
					"BillingMode": "PAY_PER_REQUEST",
					"Tags": []map[string]string{
						{"Key": "ManagedBy", "Value": "fabrica"},
						{"Key": "Name", "Value": names.Table},
					},
				},
			},
		},
	}
	return mod, nil
}

// buildHordeModule generates export resources for the Horde module.
func buildHordeModule(ms state.ModuleState, cfg *config.Config) ExportModule {
	mod := ExportModule{
		Name:   ms.Name,
		Status: ms.Status,
	}

	for _, r := range ms.Resources {
		res := ExportResource{
			TypeName:   r.TypeName,
			LogicalID:  toLogicalID(ms.Name, r.TypeName, r.Identifier),
			Properties: sanitize(extractProperties(ms.Name, r, cfg)),
			Module:     ms.Name,
		}
		mod.Resources = append(mod.Resources, res)
	}
	return mod
}

// buildPerforceModule generates export resources for the Perforce module.
func buildPerforceModule(ms state.ModuleState, cfg *config.Config) ExportModule {
	mod := ExportModule{
		Name:   ms.Name,
		Status: ms.Status,
	}

	for _, r := range ms.Resources {
		res := ExportResource{
			TypeName:   r.TypeName,
			LogicalID:  toLogicalID(ms.Name, r.TypeName, r.Identifier),
			Properties: sanitize(extractProperties(ms.Name, r, cfg)),
			Module:     ms.Name,
		}
		mod.Resources = append(mod.Resources, res)
	}
	return mod
}

// buildLoreModule generates export resources for the Lore module.
func buildLoreModule(ms state.ModuleState, cfg *config.Config) ExportModule {
	mod := ExportModule{
		Name:   ms.Name,
		Status: ms.Status,
	}

	for _, r := range ms.Resources {
		res := ExportResource{
			TypeName:   r.TypeName,
			LogicalID:  toLogicalID(ms.Name, r.TypeName, r.Identifier),
			Properties: sanitize(extractProperties(ms.Name, r, cfg)),
			Module:     ms.Name,
		}
		mod.Resources = append(mod.Resources, res)
	}
	return mod
}

// extractProperties builds resource properties from state properties and config.
// Production state stores camelCase keys (instanceType, volumeSize) in
// ModuleResource.Properties; this function normalizes them to the IaC-appropriate
// forms and enriches with config-derived defaults.
func extractProperties(moduleName string, r state.ModuleResource, cfg *config.Config) map[string]any {
	props := make(map[string]any)

	// Copy state properties, normalizing camelCase keys to their IaC forms.
	for k, v := range r.Properties {
		switch k {
		case "instanceType":
			props["InstanceType"] = v
		case "volumeSize":
			// Do NOT store volumeSize as a top-level property — it maps to
			// BlockDeviceMappings below. Store it temporarily under a private
			// key so the switch block can consume it.
			props["__volumeSize"] = v
		default:
			props[k] = v
		}
	}

	switch r.TypeName {
	case "AWS::EC2::Instance":
		if _, ok := props["InstanceType"]; !ok {
			props["InstanceType"] = instanceTypeForModule(moduleName, cfg)
		}
		if _, ok := props["ImageId"]; !ok {
			props["ImageId"] = amiIDForModule(moduleName, cfg)
		}
		// Map volumeSize → BlockDeviceMappings (not a raw EC2 property).
		if vs, ok := props["__volumeSize"]; ok {
			props["BlockDeviceMappings"] = []map[string]any{
				{
					"DeviceName": "/dev/sdf",
					"Ebs": map[string]any{
						"VolumeSize":          vs,
						"VolumeType":          "gp3",
						"DeleteOnTermination": true,
					},
				},
			}
		}
		// Drop the internal key so it never leaks into output.
		delete(props, "__volumeSize")
		if _, ok := props["Tags"]; !ok {
			props["Tags"] = defaultTags(moduleName)
		}
		if _, ok := props["MetadataOptions"]; !ok {
			props["MetadataOptions"] = map[string]any{
				"HttpTokens": "required",
			}
		}
	case "AWS::EC2::SecurityGroup":
		if _, ok := props["GroupName"]; !ok {
			props["GroupName"] = sgNameForModule(moduleName)
		}
		if _, ok := props["GroupDescription"]; !ok {
			props["GroupDescription"] = sgDescForModule(moduleName)
		}
		if _, ok := props["SecurityGroupIngress"]; !ok {
			props["SecurityGroupIngress"] = sgRulesForModule(moduleName, cfg)
		}
		if _, ok := props["Tags"]; !ok {
			props["Tags"] = defaultTags(moduleName)
		}
	case "AWS::IAM::Role":
		if _, ok := props["RoleName"]; !ok {
			props["RoleName"] = roleNameForModule(moduleName)
		}
		if _, ok := props["AssumeRolePolicyDocument"]; !ok {
			props["AssumeRolePolicyDocument"] = defaultAssumeRolePolicy()
		}
		if _, ok := props["ManagedPolicyArns"]; !ok {
			props["ManagedPolicyArns"] = []map[string]any{
				{"arn": "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"},
			}
		}
		if _, ok := props["Tags"]; !ok {
			props["Tags"] = defaultTags(moduleName)
		}
	case "AWS::IAM::InstanceProfile":
		if _, ok := props["InstanceProfileName"]; !ok {
			props["InstanceProfileName"] = profileNameForModule(moduleName)
		}
		if _, ok := props["Roles"]; !ok {
			props["Roles"] = []string{roleNameForModule(moduleName)}
		}
	}

	return props
}

// instanceTypeForModule returns the default instance type for a module from config.
func instanceTypeForModule(module string, cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	switch module {
	case "horde":
		if cfg.Horde.InstanceType != "" {
			return cfg.Horde.InstanceType
		}
		return "m7i.2xlarge"
	case "perforce":
		if cfg.Perforce.InstanceType != "" {
			return cfg.Perforce.InstanceType
		}
		return "c5.2xlarge"
	case "lore":
		if cfg.Lore.InstanceType != "" {
			return cfg.Lore.InstanceType
		}
		return "m5.xlarge"
	}
	return ""
}

// amiIDForModule returns the AMI ID for a module from config.
func amiIDForModule(module string, cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	switch module {
	case "horde":
		return cfg.Horde.AmiID
	case "lore":
		return cfg.Lore.AmiID
	}
	return ""
}

// sgNameForModule returns the default security group name for a module.
func sgNameForModule(module string) string {
	return "fabrica-" + module + "-sg"
}

// sgDescForModule returns the default security group description for a module.
func sgDescForModule(module string) string {
	return "Fabrica-managed security group for " + module
}

// sgRulesForModule returns the default security group ingress rules for a module.
func sgRulesForModule(module string, cfg *config.Config) []map[string]any {
	var cidr string
	if cfg != nil {
		switch module {
		case "horde":
			cidr = cfg.Horde.AllowedCIDR
		case "perforce":
			cidr = cfg.Perforce.AllowedCIDR
		case "lore":
			cidr = cfg.Lore.AllowedCIDR
		}
	}
	if cidr == "" {
		cidr = "10.0.0.0/8"
	}

	switch module {
	case "horde":
		return []map[string]any{
			{"IpProtocol": "tcp", "FromPort": 5000, "ToPort": 5000, "CidrIp": cidr, "Description": "Horde HTTP API + web UI"},
			{"IpProtocol": "tcp", "FromPort": 5002, "ToPort": 5002, "CidrIp": cidr, "Description": "Horde gRPC (agent connections)"},
		}
	case "perforce":
		return []map[string]any{
			{"IpProtocol": "tcp", "FromPort": 1666, "ToPort": 1666, "CidrIp": cidr, "Description": "Perforce Helix Core"},
		}
	case "lore":
		return []map[string]any{
			{"IpProtocol": "tcp", "FromPort": 41337, "ToPort": 41337, "CidrIp": cidr, "Description": "Lore gRPC"},
			{"IpProtocol": "udp", "FromPort": 41337, "ToPort": 41337, "CidrIp": cidr, "Description": "Lore QUIC"},
			{"IpProtocol": "tcp", "FromPort": 41339, "ToPort": 41339, "CidrIp": cidr, "Description": "Lore HTTP health"},
		}
	}
	return nil
}

// roleNameForModule returns the default IAM role name for a module.
func roleNameForModule(module string) string {
	return "fabrica-" + module + "-role"
}

// profileNameForModule returns the default instance profile name for a module.
func profileNameForModule(module string) string {
	return "fabrica-" + module + "-profile"
}

// defaultAssumeRolePolicy returns the standard EC2 assume role policy document.
func defaultAssumeRolePolicy() map[string]any {
	return map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Effect": "Allow",
				"Principal": map[string]any{
					"Service": "ec2.amazonaws.com",
				},
				"Action": "sts:AssumeRole",
			},
		},
	}
}

// defaultTags returns the default tag list for a module resource.
func defaultTags(module string) []map[string]string {
	return []map[string]string{
		{"Key": "ManagedBy", "Value": "fabrica"},
		{"Key": "Name", "Value": "fabrica-" + module},
		{"Key": "FabricaModule", "Value": module},
	}
}
