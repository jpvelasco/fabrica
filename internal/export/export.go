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
	"hash/fnv"
	"strings"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/ddc"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/iamrole"
	"github.com/jpvelasco/fabrica/internal/lore"
	"github.com/jpvelasco/fabrica/internal/perforce"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/workstation"
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
	TypeName  string
	LogicalID string
	// Identifier is the state-internal identifier (e.g. the DynamoDB table
	// name). Generators use it for outputs and table names that a logical ID
	// alone cannot express.
	Identifier string
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

// buildModules constructs export modules from state and config. The account
// and region come from the recorded state: inline IAM policies are
// re-derived from the same shared helpers create uses, scoped to this
// account/region.
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
		switch ms.Name {
		case "horde", "perforce", "lore", "ddc", "workstation", "ci", "deploy":
			mod := buildModule(ms, cfg, st.Account, st.Region)
			if len(mod.Resources) > 0 {
				modules = append(modules, mod)
			}
		}
	}

	return modules, nil
}

// sanitize strips credential-like fields from resource properties.
// UserData base64 blobs are replaced with a redacted placeholder. Map and
// slice values are recursed into so nested credential-like strings (and the
// string values inside inline IAM policy documents) are handled the same way.
func sanitize(props map[string]any) map[string]any {
	out := make(map[string]any, len(props))
	for k, v := range props {
		switch k {
		case "UserData":
			out[k] = "# REDACTED — cloud-init script (not included in export)"
		case "PasswordData":
			out[k] = "# REDACTED"
		default:
			out[k] = sanitizeValue(v)
		}
	}
	return out
}

// sanitizeValue applies redaction to one property value, recursing into maps
// and slices so nested credential-like strings are redacted too.
func sanitizeValue(v any) any {
	switch v := v.(type) {
	case map[string]any:
		return sanitize(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = sanitizeValue(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, len(v))
		for i, item := range v {
			out[i] = sanitize(item)
		}
		return out
	case string:
		if looksLikeBase64Blob(v) {
			return "# REDACTED — credential-like field"
		}
		return v
	default:
		return v
	}
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
// (alphanumeric, no spaces or special chars). It sanitizes the identifier to
// alphanumerics only, uppercases it, and appends an 8-char FNV-1a hash of the
// original identifier so distinct names never collide — e.g. fabrica-horde-role
// vs fabrica-horde-agents-role (both truncated to FABRICAHORDE before) now get
// unique hashes. The sanitized portion is capped at 32 chars so even long ARNs
// stay within the 255-char CFN limit; the hash suffix preserves tail entropy
// when truncation occurs. The result is deterministic and stable.
func toLogicalID(module, typeName, identifier string) string {
	var b strings.Builder
	b.Grow(len(identifier))
	for _, r := range identifier {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	sanitized := b.String()
	if sanitized == "" {
		sanitized = "X"
	}
	sanitized = strings.ToUpper(sanitized)
	const maxSanitized = 32
	if len(sanitized) > maxSanitized {
		sanitized = sanitized[:maxSanitized]
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(identifier))
	hash := fmt.Sprintf("%08X", h.Sum32())
	return fmt.Sprintf("%s%s%s%s",
		strings.ToTitle(module),
		typeNameShort(typeName),
		sanitized,
		hash)
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

// buildModule generates export resources for any module. All modules share
// the same transformation: iterate resources, assign logical IDs, sanitize
// properties, and enrich with config-derived defaults via extractProperties.
func buildModule(ms state.ModuleState, cfg *config.Config, account, region string) ExportModule {
	mod := ExportModule{
		Name:   ms.Name,
		Status: ms.Status,
	}

	// Multiple DynamoDB tables in one module (Lore S3 store tables) collide
	// on identifier-truncated logical IDs, so tables recorded with a loreTable
	// kind get a unique, stable per-suffix ID instead.
	tableLogicalIDs := loreStoreTableLogicalIDs(ms)

	for i := range ms.Resources {
		r := &ms.Resources[i]
		logicalID := toLogicalID(ms.Name, r.TypeName, r.Identifier)
		if id, ok := tableLogicalIDs[i]; ok {
			logicalID = id
		}
		mod.Resources = append(mod.Resources, ExportResource{
			TypeName:   r.TypeName,
			LogicalID:  logicalID,
			Identifier: r.Identifier,
			Properties: sanitize(extractProperties(ms.Name, *r, cfg, account, region)),
			Module:     ms.Name,
		})
	}

	return mod
}

// loreStoreTableLogicalIDs maps state-index positions of DynamoDB tables
// recorded with a loreTable kind to their per-suffix logical IDs
// (LoreTableFRAGMENTS, LoreTableMETADATA, ...). Such tables all share the
// store-bucket name prefix, so identifier-truncated IDs would collide; the
// per-suffix form is unique and stable across exports.
func loreStoreTableLogicalIDs(ms state.ModuleState) map[int]string {
	ids := make(map[int]string)
	for i := range ms.Resources {
		r := &ms.Resources[i]
		if r.TypeName != "AWS::DynamoDB::Table" {
			continue
		}
		suffix := r.Properties["loreTable"]
		if suffix == "" {
			continue
		}
		ids[i] = "LoreTable" + strings.ToUpper(suffix)
	}
	return ids
}

// extractProperties builds resource properties from state properties and config.
// Production state stores camelCase keys (instanceType, volumeSize) in
// ModuleResource.Properties; this function normalizes them to the IaC-appropriate
// forms and enriches with config-derived defaults.
// internalStateKeys are module-internal metadata recorded alongside resources
// for status, teardown, and cost logic. They are not CloudFormation/Terraform
// resource properties; leaking them into generated templates yields invalid
// IaC (e.g. "role": "agent" under AWS::EC2::Instance).
var internalStateKeys = map[string]struct{}{
	"role":              {},
	"region":            {},
	"buildVersion":      {},
	"lastBackupId":      {},
	"lastBackupAt":      {},
	"scalingPolicy":     {},
	"scalingAlarm":      {},
	"scaleOutThreshold": {},
	"scaleInThreshold":  {},
	"loreTable":         {},
}

func extractProperties(moduleName string, r state.ModuleResource, cfg *config.Config, account, region string) map[string]any {
	props := make(map[string]any)

	// Copy state properties, normalizing camelCase keys to their IaC forms
	// and dropping module-internal metadata.
	for k, v := range r.Properties {
		switch k {
		case "instanceType":
			props["InstanceType"] = v
		case "imageId":
			props["ImageId"] = v
		case "volumeSize":
			// Do NOT store volumeSize as a top-level property — it maps to
			// BlockDeviceMappings below. Store it temporarily under a private
			// key so the switch block can consume it.
			props["__volumeSize"] = v
		case "minSize":
			props["MinSize"] = v
		case "desiredCapacity":
			props["DesiredCapacity"] = v
		case "maxSize":
			props["MaxSize"] = v
		default:
			if _, internal := internalStateKeys[k]; !internal {
				props[k] = v
			}
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
		// Inline policies: re-derive the same documents the plan layer attaches
		// via iamrole.RoleDocument, so export emits what create sends to Cloud
		// Control. One document, two renderers (CFN Policies / TF
		// inline_policy). State-driven inputs (store bucket, backup S3) come
		// from config/the module's recorded resources.
		if _, ok := props["Policies"]; !ok {
			if policies := inlinePoliciesForRole(moduleName, cfg, account, region); policies != nil {
				props["Policies"] = policies
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
		// DDC/Lore bucket — properties come from state; enrich with config defaults.
		if _, ok := props["BucketName"]; !ok {
			// Use the resource identifier as the bucket name (Cloud Control
			// returns the bucket name as the resource identifier).
			props["BucketName"] = r.Identifier
		}
		if _, ok := props["Tags"]; !ok {
			props["Tags"] = defaultTags(moduleName)
		}
	case "AWS::DynamoDB::Table":
		// Module-managed DynamoDB tables (Lore S3 store tables; the state
		// backend lock table is built explicitly in buildStateBackendModule).
		// TableName comes from the recorded identifier; Lore table schemas are
		// rebuilt from the module's spec table when the suffix is known.
		props["TableName"] = r.Identifier
		if spec, ok := lore.StoreTableSpecByName(r.Properties["loreTable"]); ok {
			props["KeySchema"] = loreStoreTableKeySchema(spec)
			props["AttributeDefinitions"] = loreStoreTableAttributeDefinitions(spec)
			if len(spec.GSIs) > 0 {
				props["GlobalSecondaryIndexes"] = loreStoreTableGSIs(spec)
			}
		}
		if _, ok := props["BillingMode"]; !ok {
			props["BillingMode"] = "PAY_PER_REQUEST"
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
	case "AWS::AutoScaling::AutoScalingGroup":
		// Horde agent ASG — properties come from state (minSize, desiredCapacity,
		// maxSize, instanceType, imageId). Enrich with defaults if missing.
		if _, ok := props["AutoScalingGroupName"]; !ok {
			props["AutoScalingGroupName"] = "fabrica-" + moduleName + "-agents-asg"
		}
		if _, ok := props["Tags"]; !ok {
			props["Tags"] = defaultTags(moduleName)
		}
	case "AWS::EC2::LaunchTemplate":
		// Horde agent launch template — state stores the image/instance shape
		// flat, but the Cloud Control schema nests them under LaunchTemplateData.
		if _, ok := props["LaunchTemplateName"]; !ok {
			props["LaunchTemplateName"] = "fabrica-" + moduleName + "-agents-lt"
		}
		data := map[string]any{}
		for _, k := range []string{"InstanceType", "ImageId"} {
			if v, ok := props[k]; ok {
				data[k] = v
				delete(props, k)
			}
		}
		if len(data) > 0 {
			props["LaunchTemplateData"] = data
		}
	case "AWS::AutoScaling::ScalingPolicy":
		// Horde agent scaling policy — properties come from state.
		if _, ok := props["PolicyName"]; !ok {
			props["PolicyName"] = "fabrica-" + moduleName + "-agents-scaling-policy"
		}
		if _, ok := props["AutoScalingGroupName"]; !ok {
			props["AutoScalingGroupName"] = "fabrica-" + moduleName + "-agents-asg"
		}
	case "AWS::CloudWatch::Alarm":
		// Horde agent scaling alarm — properties come from state.
		if _, ok := props["AlarmName"]; !ok {
			// Derive alarm name from identifier (Cloud Control returns the
			// alarm name as the resource identifier).
			props["AlarmName"] = r.Identifier
		}
		if _, ok := props["AutoScalingGroupName"]; !ok {
			props["AutoScalingGroupName"] = "fabrica-" + moduleName + "-agents-asg"
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
		return horde.DefaultInstanceType
	case "perforce":
		if cfg.Perforce.InstanceType != "" {
			return cfg.Perforce.InstanceType
		}
		return perforce.DefaultInstanceType
	case "lore":
		if cfg.Lore.InstanceType != "" {
			return cfg.Lore.InstanceType
		}
		return lore.DefaultInstanceType
	case "ddc":
		if cfg.DDC.InstanceType != "" {
			return cfg.DDC.InstanceType
		}
		return ddc.DefaultInstanceType
	case "workstation":
		if cfg.Workstation.InstanceType != "" {
			return cfg.Workstation.InstanceType
		}
		return workstation.DefaultInstanceType
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
// When the config CIDR is empty, it falls back to the module's own default
// (matching what the create command would use).
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
		cidr = defaultModuleCIDR
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

// defaultModuleCIDR is the default AllowedCIDR that all module create commands
// use when the config field is empty.
const defaultModuleCIDR = "10.0.0.0/8"

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
// IAM role. EC2-based modules (perforce, horde, lore, ddc) attach SSM managed
// policies. CI and Deploy use inline policies instead, so this returns nil.
func managedPolicyARNsForModule(module string) []map[string]any {
	switch module {
	case "perforce", "horde", "lore", "ddc":
		return []map[string]any{
			{"arn": "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"},
		}
	}
	return nil
}

// inlinePoliciesForRole re-derives the inline IAM policies a module's EC2
// instance role carries, using the same shared helpers the plan layer's
// RoleDesiredState uses (one document, two renderers). Order matches the
// create path per module.
func inlinePoliciesForRole(moduleName string, cfg *config.Config, account, region string) []map[string]any {
	switch moduleName {
	case "ci":
		projectName := ciProjectNameForModule(moduleName, cfg)
		return []map[string]any{iamrole.CICodeBuildInlinePolicy(region, account, projectName)}
	case "deploy":
		bucket := ""
		if cfg != nil {
			bucket = cfg.Deploy.BuildBucket
		}
		if bucket == "" {
			return nil
		}
		return []map[string]any{iamrole.DeployS3ReadPolicy(bucket)}
	case "perforce":
		policies := []map[string]any{iamrole.SSMOutputPolicy(region, account)}
		// Same gating as the plan layer: only when backup S3 export is on and a
		// bucket is configured.
		if cfg.Perforce.Backup.S3Export && cfg.Perforce.Backup.S3Bucket != "" {
			prefix := cfg.Perforce.Backup.S3Prefix
			if prefix == "" {
				prefix = perforce.DefaultS3Prefix
			}
			policies = append(policies, iamrole.S3BucketPolicy(
				"fabrica-perforce-backup-s3",
				cfg.Perforce.Backup.S3Bucket,
				[]string{"s3:ListBucket"},
				[]string{"s3:PutObject", "s3:GetObject", "s3:DeleteObject"},
				prefix+"*",
			))
		}
		return policies
	case "horde":
		return []map[string]any{iamrole.SSMOutputPolicy(region, account)}
	case "lore":
		bucket := lore.ResolveStoreBucket(cfg.Lore.StoreBucket, account, region, cfg.Lore.StoreBackend)
		if bucket == "" {
			return nil
		}
		policies := []map[string]any{
			iamrole.S3BucketPolicy("fabrica-lore-store-s3", bucket,
				[]string{"s3:ListBucket", "s3:GetBucketLocation", "s3:ListBucketVersions"},
				[]string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:DeleteObjectVersion"},
				"*",
			),
			iamrole.SSMOutputPolicy(region, account),
			lore.StoreDynamoDBPolicy(region, account, bucket, lore.StoreTableNames(bucket)),
		}
		return policies
	case "ddc":
		// BucketOrDefault always falls back to fabrica-ddc-<account>-<region>,
		// so the bucket is never empty here.
		bucket := ddc.BucketOrDefault(cfg.DDC.Bucket, account, region)
		return []map[string]any{
			iamrole.S3BucketPolicy("fabrica-ddc-s3", bucket,
				[]string{"s3:ListBucket", "s3:GetBucketLocation"},
				[]string{"s3:GetObject", "s3:PutObject", "s3:DeleteObject"},
				"*",
			),
			iamrole.SSMOutputPolicy(region, account),
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

// loreStoreTableKeySchema renders a Lore store table spec's primary key as
// CloudFormation KeySchema entries (order matches the module's spec: the same
// source the create path provisions against).
func loreStoreTableKeySchema(spec lore.StoreTableSpec) []map[string]any {
	schema := []map[string]any{
		{"AttributeName": spec.PK, "KeyType": "HASH"},
	}
	if spec.SK != "" {
		schema = append(schema, map[string]any{"AttributeName": spec.SK, "KeyType": "RANGE"})
	}
	return schema
}

// loreStoreTableAttributeDefinitions renders the primary key attributes for
// KeySchema in CloudFormations AttributeDefinitions format.
func loreStoreTableAttributeDefinitions(spec lore.StoreTableSpec) []map[string]any {
	defs := []map[string]any{
		{"AttributeName": spec.PK, "AttributeType": spec.PKType},
	}
	if spec.SK != "" {
		defs = append(defs, map[string]any{"AttributeName": spec.SK, "AttributeType": spec.SKType})
	}
	return defs
}

// loreStoreTableGSIs renders the locks table's global secondary indexes in
// CloudFormation format. All Lore store GSIs project all attributes.
func loreStoreTableGSIs(spec lore.StoreTableSpec) []map[string]any {
	gsis := make([]map[string]any, 0, len(spec.GSIs))
	for _, gsi := range spec.GSIs {
		keySchema := []map[string]any{
			{"AttributeName": gsi.PK, "KeyType": "HASH"},
			{"AttributeName": gsi.SK, "KeyType": "RANGE"},
		}
		gsis = append(gsis, map[string]any{
			"IndexName":      gsi.Name,
			"KeySchema":      keySchema,
			"ProjectionType": "ALL",
		})
	}
	return gsis
}

// ciProjectNameForModule returns the CodeBuild project name for the CI module.
func ciProjectNameForModule(module string, cfg *config.Config) string {
	if cfg != nil && cfg.CI.ProjectName != "" {
		return cfg.CI.ProjectName
	}
	return "fabrica-" + module
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
