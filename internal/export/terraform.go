package export

import (
	"fmt"
	"strings"
)

// terraformGenerator produces Terraform HCL configuration from export modules.
type terraformGenerator struct{}

// Generate renders the modules as Terraform HCL.
func (g *terraformGenerator) Generate(modules []ExportModule) ([]byte, error) {
	var sb strings.Builder

	sb.WriteString("# Fabrica-managed infrastructure — exported from local state.\n")
	fmt.Fprintf(&sb, "# Modules: %s (V1)\n", moduleNames(modules))
	sb.WriteString("#\n")
	sb.WriteString("# Generated from Fabrica local state — review before applying.\n")
	sb.WriteString("# UserData and credentials are redacted.\n")
	sb.WriteString("\n")

	// Provider block.
	sb.WriteString("terraform {\n")
	sb.WriteString("  required_providers {\n")
	sb.WriteString("    aws = {\n")
	sb.WriteString(`      source = "hashicorp/aws"` + "\n")
	sb.WriteString("      version = \">= 5.0\"\n")
	sb.WriteString("    }\n")
	sb.WriteString("  }\n")
	sb.WriteString("}\n")
	sb.WriteString("\n")

	// Resource blocks.
	for _, mod := range modules {
		fmt.Fprintf(&sb, "# Module: %s (status: %s)\n", mod.Name, mod.Status)
		for _, res := range mod.Resources {
			block, err := g.resourceToHCL(res)
			if err != nil {
				return nil, fmt.Errorf("converting %s/%s: %w", res.Module, res.LogicalID, err)
			}
			sb.WriteString(block)
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	// Outputs.
	sb.WriteString("# Outputs\n")
	for _, mod := range modules {
		for _, res := range mod.Resources {
			g.addOutputHCL(&sb, res, mod.Name)
		}
	}

	return []byte(sb.String()), nil
}

// resourceToHCL converts an ExportResource to a Terraform resource block.
func (g *terraformGenerator) resourceToHCL(res ExportResource) (string, error) {
	resourceType := g.tfResourceType(res.TypeName)
	resourceName := g.tfResourceName(res.LogicalID)

	var sb strings.Builder
	fmt.Fprintf(&sb, "resource \"%s\" \"%s\" {\n", resourceType, resourceName)

	for k, v := range res.Properties {
		// Skip redacted fields.
		if s, ok := v.(string); ok && strings.HasPrefix(s, "# REDACTED") {
			continue
		}

		switch k {
		case "Tags":
			sb.WriteString(g.hclTags(v))
		case "SecurityGroupIngress":
			sb.WriteString(g.hclSGIngress(v))
		case "AssumeRolePolicyDocument":
			sb.WriteString(g.hclPolicyDoc(v))
		case "ManagedPolicyArns":
			sb.WriteString(g.hclPolicyArns(v))
		case "BlockDeviceMappings":
			sb.WriteString(g.hclBlockDevices(v))
		case "KeySchema":
			sb.WriteString(g.hclKeySchema(v))
		case "AttributeDefinitions":
			sb.WriteString(g.hclAttrDefs(v))
		case "VersioningConfiguration":
			sb.WriteString(g.hclVersioning(v))
		case "BucketEncryption":
			sb.WriteString(g.hclEncryption(v))
		case "PublicAccessBlockConfiguration":
			sb.WriteString(g.hclPublicAccess(v))
		case "MetadataOptions":
			sb.WriteString(g.hclMetadataOptions(v))
		case "Roles":
			sb.WriteString(g.hclRoles(v))
		default:
			sb.WriteString(g.hclScalar(k, v))
		}
	}

	sb.WriteString("}\n")
	return sb.String(), nil
}

// tfResourceType converts a CloudFormation type to a Terraform resource type.
func (g *terraformGenerator) tfResourceType(cfType string) string {
	switch cfType {
	case "AWS::EC2::Instance":
		return "aws_instance"
	case "AWS::EC2::SecurityGroup":
		return "aws_security_group"
	case "AWS::EC2::Volume":
		return "aws_ebs_volume"
	case "AWS::IAM::Role":
		return "aws_iam_role"
	case "AWS::IAM::InstanceProfile":
		return "aws_iam_instance_profile"
	case "AWS::S3::Bucket":
		return "aws_s3_bucket"
	case "AWS::DynamoDB::Table":
		return "aws_dynamodb_table"
	default:
		// Generic conversion: AWS::Service::Resource → aws_service_resource
		parts := strings.Split(cfType, "::")
		if len(parts) == 3 {
			return "aws_" + strings.ToLower(parts[1]) + "_" + strings.ToLower(parts[2])
		}
		return "aws_" + strings.ToLower(parts[len(parts)-1])
	}
}

// tfResourceName converts a logical ID to a Terraform resource name.
func (g *terraformGenerator) tfResourceName(logicalID string) string {
	// Convert "HordeSecurityGroupI" to "horde_security_group_i"
	var sb strings.Builder
	for i, r := range logicalID {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				sb.WriteString("_")
			}
			sb.WriteString(string(r + 'a' - 'A'))
		} else {
			sb.WriteString(string(r))
		}
	}
	return sb.String()
}

// hclTags converts tags to HCL format.
func (g *terraformGenerator) hclTags(v any) string {
	tags, ok := v.([]map[string]string)
	if !ok {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("  tags = {\n")
	for _, t := range tags {
		fmt.Fprintf(&sb, "    %s = %q\n", t["Key"], t["Value"])
	}
	sb.WriteString("  }\n")
	return sb.String()
}

// hclSGIngress converts security group ingress rules to HCL format.
func (g *terraformGenerator) hclSGIngress(v any) string {
	rules, ok := v.([]map[string]any)
	if !ok {
		return ""
	}
	var sb strings.Builder
	for i, rule := range rules {
		sb.WriteString("  ingress {\n")
		fmt.Fprintf(&sb, "    protocol = %q\n", rule["IpProtocol"])
		fmt.Fprintf(&sb, "    from_port = %v\n", rule["FromPort"])
		fmt.Fprintf(&sb, "    to_port   = %v\n", rule["ToPort"])
		fmt.Fprintf(&sb, "    cidr_blocks = [%q]\n", rule["CidrIp"])
		if desc, ok := rule["Description"].(string); ok && desc != "" {
			fmt.Fprintf(&sb, "    description = %q\n", desc)
		}
		sb.WriteString("  }\n")
		if i < len(rules)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// hclPolicyDoc converts assume role policy document to HCL format.
func (g *terraformGenerator) hclPolicyDoc(v any) string {
	doc, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("  assume_role_policy = jsonencode({\n")
	fmt.Fprintf(&sb, "    Version = %q\n", doc["Version"])
	sb.WriteString("    Statement = [{\n")
	fmt.Fprintf(&sb, "      Effect    = %q\n", "Allow")
	sb.WriteString("      Principal = {\n")
	fmt.Fprintf(&sb, "        Service = %q\n", "ec2.amazonaws.com")
	sb.WriteString("      }\n")
	fmt.Fprintf(&sb, "      Action = %q\n", "sts:AssumeRole")
	sb.WriteString("    }]\n")
	sb.WriteString("  })\n")
	return sb.String()
}

// hclPolicyArns converts managed policy ARNs to HCL format.
func (g *terraformGenerator) hclPolicyArns(v any) string {
	if arns, ok := v.([]map[string]any); ok {
		var sb strings.Builder
		sb.WriteString("  managed_policy_arns = [\n")
		for _, a := range arns {
			if s, ok := a["arn"].(string); ok {
				fmt.Fprintf(&sb, "    %q,\n", s)
			}
		}
		sb.WriteString("  ]\n")
		return sb.String()
	}
	if arns, ok := v.([]string); ok {
		var sb strings.Builder
		sb.WriteString("  managed_policy_arns = [\n")
		for _, a := range arns {
			fmt.Fprintf(&sb, "    %q,\n", a)
		}
		sb.WriteString("  ]\n")
		return sb.String()
	}
	return ""
}

// hclBlockDevices converts block device mappings to HCL format.
func (g *terraformGenerator) hclBlockDevices(v any) string {
	devs, ok := v.([]map[string]any)
	if !ok {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("  ebs_block_device {\n")
	for _, dev := range devs {
		fmt.Fprintf(&sb, "    device_name = %q\n", dev["DeviceName"])
		if ebs, ok := dev["Ebs"].(map[string]any); ok {
			fmt.Fprintf(&sb, "    volume_size = %v\n", ebs["VolumeSize"])
			fmt.Fprintf(&sb, "    volume_type = %q\n", ebs["VolumeType"])
			if del, ok := ebs["DeleteOnTermination"].(bool); ok {
				fmt.Fprintf(&sb, "    delete_on_termination = %t\n", del)
			}
		}
	}
	sb.WriteString("  }\n")
	return sb.String()
}

// hclKeySchema converts DynamoDB key schema to HCL format.
func (g *terraformGenerator) hclKeySchema(v any) string {
	ks, ok := v.([]map[string]any)
	if !ok {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "  hash_key = %q\n", ks[0]["AttributeName"])
	return sb.String()
}

// hclAttrDefs converts DynamoDB attribute definitions to HCL format.
func (g *terraformGenerator) hclAttrDefs(v any) string {
	ad, ok := v.([]map[string]any)
	if !ok {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("  attribute {\n")
	for _, a := range ad {
		fmt.Fprintf(&sb, "    name = %q\n", a["AttributeName"])
		fmt.Fprintf(&sb, "    type = %q\n", a["AttributeType"])
	}
	sb.WriteString("  }\n")
	return sb.String()
}

// hclVersioning converts S3 versioning to HCL format.
func (g *terraformGenerator) hclVersioning(v any) string {
	vc, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("  versioning {\n")
	fmt.Fprintf(&sb, "    enabled = %t\n", vc["Status"] == "Enabled")
	sb.WriteString("  }\n")
	return sb.String()
}

// hclEncryption converts S3 bucket encryption to HCL format.
func (g *terraformGenerator) hclEncryption(v any) string {
	be, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("  server_side_encryption_configuration {\n")
	sb.WriteString("    rule {\n")
	sb.WriteString("      apply_server_side_encryption_by_default {\n")
	if sse, ok := be["ServerSideEncryptionConfiguration"].([]map[string]any); ok && len(sse) > 0 {
		if defaultEnc, ok := sse[0]["ServerSideEncryptionByDefault"].(map[string]any); ok {
			fmt.Fprintf(&sb, "        sse_algorithm = %q\n", defaultEnc["SSEAlgorithm"])
		}
	}
	sb.WriteString("      }\n")
	sb.WriteString("    }\n")
	sb.WriteString("  }\n")
	return sb.String()
}

// hclPublicAccess converts S3 public access block to HCL format.
func (g *terraformGenerator) hclPublicAccess(v any) string {
	_, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("  # Public access block — all blocked\n")
	return sb.String()
}

// hclMetadataOptions converts EC2 metadata options to HCL format.
func (g *terraformGenerator) hclMetadataOptions(v any) string {
	mo, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("  metadata_options {\n")
	fmt.Fprintf(&sb, "    http_tokens = %q\n", mo["HttpTokens"])
	sb.WriteString("  }\n")
	return sb.String()
}

// hclRoles converts IAM instance profile roles to HCL format.
func (g *terraformGenerator) hclRoles(v any) string {
	roles, ok := v.([]string)
	if !ok {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("  role = ")
	fmt.Fprintf(&sb, "%q\n", roles[0])
	return sb.String()
}

// hclScalar converts a scalar property to HCL format.
func (g *terraformGenerator) hclScalar(k string, v any) string {
	hclKey := g.tfAttributeName(k)
	var sb strings.Builder

	switch val := v.(type) {
	case string:
		fmt.Fprintf(&sb, "  %s = %q\n", hclKey, val)
	case int:
		fmt.Fprintf(&sb, "  %s = %d\n", hclKey, val)
	case int64:
		fmt.Fprintf(&sb, "  %s = %d\n", hclKey, val)
	case float64:
		fmt.Fprintf(&sb, "  %s = %.0f\n", hclKey, val)
	case bool:
		fmt.Fprintf(&sb, "  %s = %t\n", hclKey, val)
	default:
		fmt.Fprintf(&sb, "  # %s = (complex type, see CloudFormation export)\n", hclKey)
	}

	return sb.String()
}

// tfAttributeName converts a CloudFormation attribute name to a Terraform attribute name.
func (g *terraformGenerator) tfAttributeName(cfAttr string) string {
	switch cfAttr {
	case "BucketName":
		return "bucket"
	case "TableName":
		return "name"
	case "InstanceType":
		return "instance_type"
	case "ImageId":
		return "ami"
	case "SubnetId":
		return "subnet_id"
	case "SecurityGroupIds":
		return "vpc_security_group_ids"
	case "SecurityGroupId":
		return "vpc_security_group_id"
	case "VpcId":
		return "vpc_id"
	case "GroupName":
		return "name"
	case "GroupDescription":
		return "description"
	case "RoleName":
		return "name"
	case "InstanceProfileName":
		return "name"
	case "BillingMode":
		return "billing_mode"
	case "ManagedPolicyArns":
		return "managed_policy_arns"
	case "AssumeRolePolicyDocument":
		return "assume_role_policy"
	case "KeySchema":
		return "hash_key"
	case "AttributeDefinitions":
		return "attribute"
	case "VersioningConfiguration":
		return "versioning"
	case "BucketEncryption":
		return "server_side_encryption_configuration"
	case "PublicAccessBlockConfiguration":
		return "public_access_block"
	case "MetadataOptions":
		return "metadata_options"
	case "BlockDeviceMappings":
		return "ebs_block_device"
	case "SecurityGroupIngress":
		return "ingress"
	case "Tags":
		return "tags"
	case "Roles":
		return "role"
	default:
		// Convert CamelCase to snake_case.
		var sb strings.Builder
		for i, r := range cfAttr {
			if r >= 'A' && r <= 'Z' {
				if i > 0 {
					sb.WriteString("_")
				}
				sb.WriteString(string(r + 'a' - 'A'))
			} else {
				sb.WriteString(string(r))
			}
		}
		return sb.String()
	}
}

// addOutputHCL adds Terraform output blocks.
func (g *terraformGenerator) addOutputHCL(sb *strings.Builder, res ExportResource, moduleName string) {
	var outputName string
	var value string
	var desc string

	switch res.TypeName {
	case "AWS::EC2::Instance":
		outputName = g.tfResourceName(res.LogicalID) + "_instance_id"
		value = fmt.Sprintf("${%s.%s.id}", g.tfResourceType(res.TypeName), g.tfResourceName(res.LogicalID))
		desc = fmt.Sprintf("%s instance ID", moduleName)
	case "AWS::EC2::SecurityGroup":
		outputName = g.tfResourceName(res.LogicalID) + "_id"
		value = fmt.Sprintf("${%s.%s.id}", g.tfResourceType(res.TypeName), g.tfResourceName(res.LogicalID))
		desc = fmt.Sprintf("%s security group ID", moduleName)
	case "AWS::S3::Bucket":
		outputName = "state_bucket_name"
		value = "${aws_s3_bucket.fabrica_state_bucket.id}"
		desc = "Fabrica state S3 bucket name"
	case "AWS::DynamoDB::Table":
		outputName = "state_lock_table_name"
		value = "${aws_dynamodb_table.fabrica_state_lock_table.id}"
		desc = "Fabrica state lock DynamoDB table name"
	case "AWS::IAM::Role":
		outputName = g.tfResourceName(res.LogicalID) + "_arn"
		value = fmt.Sprintf("${%s.%s.arn}", g.tfResourceType(res.TypeName), g.tfResourceName(res.LogicalID))
		desc = fmt.Sprintf("%s IAM role ARN", moduleName)
	default:
		return
	}

	fmt.Fprintf(sb, "output \"%s\" {\n", outputName)
	fmt.Fprintf(sb, "  description = %q\n", desc)
	fmt.Fprintf(sb, "  value       = %s\n", value)
	sb.WriteString("}\n\n")
}
