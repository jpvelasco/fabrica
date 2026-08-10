// Package export provides infrastructure-as-code generation from Fabrica's
// recorded state and configuration. It converts Fabrica's internal plan and
// state data into CloudFormation YAML and Terraform HCL formats.
//
// V2 scope: all modules — state backend (S3 bucket + DynamoDB table), Horde,
// Perforce, Lore, DDC (home + edge), Workstation, CI, and Deploy.
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
		case "ddc":
			mod = buildDDCModule(ms, cfg)
		case "workstation":
			mod = buildWorkstationModule(ms, cfg)
		case "ci":
			mod = buildCIModule(ms, cfg)
		case "deploy":
			mod = buildDeployModule(ms, cfg)
		default:
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
// (alphanumeric, no spaces or special chars). It uses the full sanitized
// identifier (truncated to 12 chars) so that multiple resources of the same
// type within one module get distinct IDs — e.g. DDC home vs. edge SGs,
// or Deploy fleets and builds.
func toLogicalID(module, typeName, identifier string) string {
	id := strings.ReplaceAll(identifier, "-", "")
	id = strings.ReplaceAll(id, "_", "")
	id = strings.ReplaceAll(id, ".", "")

	if len(id) == 0 {
		id = "X"
	}

	if len(id) > 12 {
		id = id[:12]
	}

	return fmt.Sprintf("%s%s%s",
		strings.ToTitle(module),
		typeNameShort(typeName),
		strings.ToUpper(id))
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
				Module:    "state-backend",
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
				Module:    "state-backend",
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

// buildDDCModule generates export resources for the DDC module.
// Covers home-region resources (IAM role, instance profile, S3 bucket, SG,
// EC2 instances) plus any edge-region SGs and instances recorded in state.
func buildDDCModule(ms state.ModuleState, cfg *config.Config) ExportModule {
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

// buildWorkstationModule generates export resources for the Workstation module.
// Covers SG + EC2 instance (NICE DCV).
func buildWorkstationModule(ms state.ModuleState, cfg *config.Config) ExportModule {
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

// buildCIModule generates export resources for the CI module.
// Covers IAM role (Cloud Control) and CodeBuild project (SDK-backed).
func buildCIModule(ms state.ModuleState, cfg *config.Config) ExportModule {
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

// buildDeployModule generates export resources for the Deploy module.
// Covers IAM role, GameLift alias, fleets, and builds as recorded in state.
func buildDeployModule(ms state.ModuleState, cfg *config.Config) ExportModule {
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
					"DeviceName": deviceNameForModule(moduleName),
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
			props["AssumeRolePolicyDocument"] = assumeRolePolicyForModule(moduleName)
		}
		// Only attach SSM managed policy for EC2-based modules (perforce, ddc).
		// CI uses inline policies; Deploy uses GameLift-managed permissions.
		if _, ok := props["ManagedPolicyArns"]; !ok {
			if managedPolicyARNs := managedPolicyARNsForModule(moduleName); managedPolicyARNs != nil {
				props["ManagedPolicyArns"] = managedPolicyARNs
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
	case "AWS::S3::Bucket":
		// DDC bucket — properties come from state; enrich with config defaults.
		if _, ok := props["BucketName"]; !ok {
			props["BucketName"] = "fabrica-" + moduleName + "-bucket"
		}
		if _, ok := props["Tags"]; !ok {
			props["Tags"] = defaultTags(moduleName)
		}
	case "AWS::CodeBuild::Project":
		// CI CodeBuild project — SDK-backed, so state may have limited properties.
		if _, ok := props["Name"]; !ok {
			props["Name"] = ciProjectNameForModule(moduleName, cfg)
		}
		// NOTE: ServiceRole omitted — the ARN is account-specific and not
		// reconstructible from local state. Add the ServiceRole ARN manually
		// before applying (e.g. arn:aws:iam::<account>:role/fabrica-ci-codebuild).
		if _, ok := props["Artifacts"]; !ok {
			props["Artifacts"] = map[string]any{
				"Type": "NO_ARTIFACTS",
			}
		}
		if _, ok := props["Environment"]; !ok {
			props["Environment"] = map[string]any{
				"ComputeType": ciComputeTypeForModule(cfg),
				"Image":       ciImageForModule(cfg),
				"Type":        "LINUX_CONTAINER",
			}
		}
	case "AWS::GameLift::Alias":
		if _, ok := props["Name"]; !ok {
			props["Name"] = deployAliasNameForModule(moduleName, cfg)
		}
	case "AWS::GameLift::Fleet":
		if _, ok := props["Name"]; !ok {
			props["Name"] = "fabrica-" + moduleName + "-fleet"
		}
		if _, ok := props["Tags"]; !ok {
			props["Tags"] = defaultTags(moduleName)
		}
	case "AWS::GameLift::Build":
		if _, ok := props["Name"]; !ok {
			props["Name"] = "fabrica-" + moduleName + "-build"
		}
	}

	return props
}

// deviceNameForModule returns the EBS device name for a module.
// Workstation uses /dev/sda1 (root EBS); all other modules use /dev/sdf.
func deviceNameForModule(module string) string {
	if module == "workstation" {
		return "/dev/sda1"
	}
	return "/dev/sdf"
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
	case "ddc":
		if cfg.DDC.InstanceType != "" {
			return cfg.DDC.InstanceType
		}
		return "m5.xlarge"
	case "workstation":
		if cfg.Workstation.InstanceType != "" {
			return cfg.Workstation.InstanceType
		}
		return "g4dn.xlarge"
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
	case "ddc":
		return cfg.DDC.AmiID
	case "workstation":
		return cfg.Workstation.AmiID
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
		case "ddc":
			cidr = cfg.DDC.AllowedCIDR
		case "workstation":
			cidr = cfg.Workstation.AllowedCIDR
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
	case "ddc":
		return []map[string]any{
			{"IpProtocol": "tcp", "FromPort": 80, "ToPort": 80, "CidrIp": cidr, "Description": "DDC public API"},
			{"IpProtocol": "tcp", "FromPort": 8080, "ToPort": 8080, "CidrIp": cidr, "Description": "DDC internal API"},
		}
	case "workstation":
		return []map[string]any{
			{"IpProtocol": "tcp", "FromPort": 8443, "ToPort": 8443, "CidrIp": cidr, "Description": "NICE DCV HTTPS"},
		}
	}
	return nil
}

// roleNameForModule returns the default IAM role name for a module.
// CI uses fabrica-ci-codebuild and Deploy uses fabrica-deploy-gamelift
// to match the production identifiers; all other modules use fabrica-<mod>-role.
func roleNameForModule(module string) string {
	switch module {
	case "ci":
		return "fabrica-ci-codebuild"
	case "deploy":
		return "fabrica-deploy-gamelift"
	}
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

// assumeRolePolicyForModule returns the trust policy document for a module's
// IAM role. EC2-based modules (perforce, ddc) trust ec2.amazonaws.com;
// CI trusts codebuild.amazonaws.com; Deploy trusts gamelift.amazonaws.com.
func assumeRolePolicyForModule(module string) map[string]any {
	service := "ec2.amazonaws.com"
	switch module {
	case "ci":
		service = "codebuild.amazonaws.com"
	case "deploy":
		service = "gamelift.amazonaws.com"
	}
	return map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Effect": "Allow",
				"Principal": map[string]any{
					"Service": service,
				},
				"Action": "sts:AssumeRole",
			},
		},
	}
}

// managedPolicyARNsForModule returns the managed policy ARNs for a module's
// IAM role. Only EC2-based modules (perforce, ddc) attach SSM managed policies.
// CI and Deploy use inline policies instead, so this returns nil for them.
func managedPolicyARNsForModule(module string) []map[string]any {
	switch module {
	case "perforce", "ddc":
		return []map[string]any{
			{"arn": "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"},
		}
	}
	return nil
}

// defaultTags returns the default tag list for a module resource.
func defaultTags(module string) []map[string]string {
	return []map[string]string{
		{"Key": "ManagedBy", "Value": "fabrica"},
		{"Key": "Name", "Value": "fabrica-" + module},
		{"Key": "FabricaModule", "Value": module},
	}
}

// ciProjectNameForModule returns the CodeBuild project name for the CI module.
func ciProjectNameForModule(module string, cfg *config.Config) string {
	if cfg != nil && cfg.CI.ProjectName != "" {
		return cfg.CI.ProjectName
	}
	return "fabrica-" + module
}

// ciRoleNameForModule returns the CI IAM role name.
func ciRoleNameForModule(module string) string {
	return "fabrica-" + module + "-codebuild"
}

// ciComputeTypeForModule returns the CI compute type from config.
func ciComputeTypeForModule(cfg *config.Config) string {
	if cfg != nil && cfg.CI.ComputeType != "" {
		return cfg.CI.ComputeType
	}
	return "BUILD_GENERAL1_SMALL"
}

// ciImageForModule returns the CI Docker image from config.
func ciImageForModule(cfg *config.Config) string {
	if cfg != nil && cfg.CI.Image != "" {
		return cfg.CI.Image
	}
	return "aws/codebuild/amazonlinux2-x86_64-standard:5.0"
}

// deployAliasNameForModule returns the GameLift alias name for the deploy module.
func deployAliasNameForModule(module string, cfg *config.Config) string {
	if cfg != nil && cfg.Deploy.AliasName != "" {
		return cfg.Deploy.AliasName
	}
	return "fabrica-" + module + "-alias"
}
