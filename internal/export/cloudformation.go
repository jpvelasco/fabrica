package export

import (
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// cloudFormationGenerator produces CloudFormation YAML templates.
type cloudFormationGenerator struct{}

// cfTemplate is the top-level CloudFormation template structure.
type cfTemplate struct {
	AWSTemplateFormatVersion string                    `yaml:"AWSTemplateFormatVersion"`
	Description              string                    `yaml:"Description"`
	Metadata                 map[string]any            `yaml:"Metadata,omitempty"`
	Resources                map[string]map[string]any `yaml:"Resources"`
	Outputs                  map[string]cfOutput       `yaml:"Outputs,omitempty"`
}

// cfOutput is a CloudFormation output definition.
type cfOutput struct {
	Description string    `yaml:"Description"`
	Value       any       `yaml:"Value"`
	Export      *cfExport `yaml:"Export,omitempty"`
}

// cfExport is the Export.Name field in a CloudFormation output.
type cfExport struct {
	Name string `yaml:"Name"`
}

// Generate renders the modules as a CloudFormation YAML template.
func (g *cloudFormationGenerator) Generate(modules []ExportModule) ([]byte, error) {
	tmpl := cfTemplate{
		AWSTemplateFormatVersion: "2010-09-09",
		Description:              fmt.Sprintf("Fabrica-managed infrastructure — exported from local state. Modules: %s (V2)", moduleNames(modules)),
		Metadata: map[string]any{
			"FabricaExport": map[string]any{
				"Version":    "v2",
				"Modules":    moduleNames(modules),
				"Disclaimer": "Generated from Fabrica local state — verify before applying. UserData and credentials are redacted.",
			},
		},
		Resources: make(map[string]map[string]any),
		Outputs:   make(map[string]cfOutput),
	}

	for _, mod := range modules {
		for _, res := range mod.Resources {
			resourceMap, err := g.resourceToCF(res, modules)
			if err != nil {
				return nil, fmt.Errorf("converting %s/%s: %w", res.Module, res.LogicalID, err)
			}
			tmpl.Resources[res.LogicalID] = resourceMap
			g.addOutput(&tmpl, res, mod.Name)
		}
	}

	data, err := yaml.Marshal(tmpl)
	if err != nil {
		return nil, fmt.Errorf("marshaling CloudFormation YAML: %w", err)
	}
	return data, nil
}

// resourceToCF converts an ExportResource to a CloudFormation resource map.
func (g *cloudFormationGenerator) resourceToCF(res ExportResource, modules []ExportModule) (map[string]any, error) {
	resourceMap := map[string]any{
		"Type":       res.TypeName,
		"Properties": g.propertiesToCF(res.Properties),
	}

	if deps := g.resolveDependencies(res, modules); len(deps) > 0 {
		resourceMap["DependsOn"] = deps
	}

	return resourceMap, nil
}

// propertiesToCF converts export properties to CloudFormation-compatible values.
func (g *cloudFormationGenerator) propertiesToCF(props map[string]any) map[string]any {
	out := make(map[string]any, len(props))

	for k, v := range props {
		// Skip redacted fields.
		if s, ok := v.(string); ok && strings.HasPrefix(s, "# REDACTED") {
			continue
		}

		switch k {
		case "SecurityGroupIngress":
			out[k] = cfIngressRules(v)
		case "Tags":
			out[k] = cfTags(v)
		case "AssumeRolePolicyDocument":
			out[k] = v
		case "ManagedPolicyArns":
			out[k] = cfPolicyArns(v)
		case "BlockDeviceMappings":
			out[k] = cfBlockDevices(v)
		case "KeySchema":
			out[k] = v
		case "AttributeDefinitions":
			out[k] = v
		case "VersioningConfiguration":
			out[k] = v
		case "BucketEncryption":
			out[k] = v
		case "PublicAccessBlockConfiguration":
			out[k] = v
		case "MetadataOptions":
			out[k] = v
		case "Roles":
			if roles, ok := v.([]string); ok {
				out[k] = roles
			}
		default:
			out[k] = v
		}
	}

	return out
}

// cfIngressRules converts security group ingress rules to CloudFormation format.
func cfIngressRules(v any) []map[string]any {
	rules, ok := v.([]map[string]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, len(rules))
	for i, r := range rules {
		out[i] = map[string]any{
			"IpProtocol": r["IpProtocol"],
			"FromPort":   r["FromPort"],
			"ToPort":     r["ToPort"],
			"CidrIp":     r["CidrIp"],
		}
		if desc, ok := r["Description"].(string); ok && desc != "" {
			out[i]["Description"] = desc
		}
	}
	return out
}

// cfTags converts tag list to CloudFormation format.
func cfTags(v any) []map[string]any {
	tags, ok := v.([]map[string]string)
	if !ok {
		return nil
	}
	out := make([]map[string]any, len(tags))
	for i, t := range tags {
		out[i] = map[string]any{
			"Key":   t["Key"],
			"Value": t["Value"],
		}
	}
	return out
}

// cfPolicyArns converts managed policy ARNs to CloudFormation format.
func cfPolicyArns(v any) []string {
	if arns, ok := v.([]map[string]any); ok {
		out := make([]string, len(arns))
		for i, a := range arns {
			if s, ok := a["arn"].(string); ok {
				out[i] = s
			}
		}
		return out
	}
	if arns, ok := v.([]string); ok {
		return arns
	}
	return nil
}

// cfBlockDevices converts block device mappings.
func cfBlockDevices(v any) []map[string]any {
	devs, ok := v.([]map[string]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, len(devs))
	for i, dev := range devs {
		if ebs, ok := dev["Ebs"].(map[string]any); ok {
			out[i] = map[string]any{
				"DeviceName": dev["DeviceName"],
				"Ebs": map[string]any{
					"VolumeSize":          ebs["VolumeSize"],
					"VolumeType":          ebs["VolumeType"],
					"DeleteOnTermination": ebs["DeleteOnTermination"],
				},
			}
		}
	}
	return out
}

// resolveDependencies determines CloudFormation DependsOn for a resource.
func (g *cloudFormationGenerator) resolveDependencies(r ExportResource, modules []ExportModule) []string {
	var deps []string

	if r.TypeName == "AWS::EC2::Instance" {
		sgID := findResourceID(modules, r.Module, "AWS::EC2::SecurityGroup")
		if sgID != "" {
			deps = append(deps, sgID)
		}
	}

	if r.TypeName == "AWS::IAM::InstanceProfile" {
		roleID := findResourceID(modules, r.Module, "AWS::IAM::Role")
		if roleID != "" {
			deps = append(deps, roleID)
		}
	}

	return deps
}

// findResourceID finds a resource logical ID by module and type.
func findResourceID(modules []ExportModule, module, typeName string) string {
	for _, mod := range modules {
		if module != "" && mod.Name != module {
			continue
		}
		for _, r := range mod.Resources {
			if r.TypeName == typeName {
				return r.LogicalID
			}
		}
	}
	return ""
}

// addOutput adds CloudFormation outputs for key resources.
func (g *cloudFormationGenerator) addOutput(t *cfTemplate, r ExportResource, moduleName string) {
	var outputName string
	var value any
	var desc string

	switch r.TypeName {
	case "AWS::EC2::Instance":
		outputName = r.LogicalID + "InstanceID"
		value = map[string]any{"Ref": r.LogicalID}
		desc = fmt.Sprintf("%s instance ID", moduleName)
	case "AWS::EC2::SecurityGroup":
		outputName = r.LogicalID + "SecurityGroupID"
		value = map[string]any{"Ref": r.LogicalID}
		desc = fmt.Sprintf("%s security group ID", moduleName)
	case "AWS::S3::Bucket":
		if r.Module == "state-backend" {
			outputName = "StateBucketName"
			value = map[string]any{"Ref": r.LogicalID}
			desc = "Fabrica state S3 bucket name"
		} else {
			outputName = r.LogicalID + "BucketName"
			value = map[string]any{"Ref": r.LogicalID}
			desc = fmt.Sprintf("%s S3 bucket name", moduleName)
		}
	case "AWS::DynamoDB::Table":
		value = map[string]any{"Ref": r.LogicalID}
		if r.Module == "state-backend" {
			// The state lock table keeps its dedicated output name.
			outputName = "StateLockTableName"
			desc = "Fabrica state lock DynamoDB table name"
		} else {
			// Module-managed tables (Lore S3 store tables) get per-table
			// outputs; logical IDs are distinct, so the names are too.
			outputName = ddbTableNameOutput(r.LogicalID)
			desc = fmt.Sprintf("%s %s DynamoDB table name", r.Module, r.Identifier)
		}
	case "AWS::IAM::Role":
		outputName = r.LogicalID + "RoleARN"
		value = map[string]any{"Fn::GetAtt": []any{r.LogicalID, "Arn"}}
		desc = fmt.Sprintf("%s IAM role ARN", moduleName)
	case "AWS::CodeBuild::Project":
		outputName = r.LogicalID + "ProjectName"
		value = map[string]any{"Ref": r.LogicalID}
		desc = fmt.Sprintf("%s CodeBuild project name", moduleName)
	case "AWS::GameLift::Alias":
		outputName = r.LogicalID + "AliasID"
		value = map[string]any{"Ref": r.LogicalID}
		desc = fmt.Sprintf("%s GameLift alias ID", moduleName)
	case "AWS::GameLift::Fleet":
		outputName = r.LogicalID + "FleetID"
		value = map[string]any{"Ref": r.LogicalID}
		desc = fmt.Sprintf("%s GameLift fleet ID", moduleName)
	case "AWS::GameLift::Build":
		outputName = r.LogicalID + "BuildID"
		value = map[string]any{"Ref": r.LogicalID}
		desc = fmt.Sprintf("%s GameLift build ID", moduleName)
	default:
		return
	}

	t.Outputs[outputName] = cfOutput{
		Description: desc,
		Value:       value,
		Export:      &cfExport{Name: outputName},
	}
}

// moduleNames returns a comma-separated list of module names.
// ddbTableNameOutput maps a DynamoDB table's logical ID to its table-name
// output. Lore store tables get stable per-suffix names (LoreTableFRAGMENTS →
// LoreStoreFragmentsTableName); any other module table falls back to the
// logical-ID form.
func ddbTableNameOutput(logicalID string) string {
	for _, suffix := range []string{"fragments", "metadata", "mutable", "locks"} {
		if logicalID == "LoreTable"+strings.ToUpper(suffix) {
			return "LoreStore" + strings.ToUpper(suffix[:1]) + suffix[1:] + "TableName"
		}
	}
	return logicalID + "TableName"
}

func moduleNames(modules []ExportModule) string {
	names := make([]string, len(modules))
	for i, m := range modules {
		names[i] = m.Name
	}
	return strings.Join(names, ", ")
}
