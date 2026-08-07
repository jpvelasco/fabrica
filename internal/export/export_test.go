package export

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/state"
	"go.yaml.in/yaml/v3"
)

func testStateWithHorde() *state.State {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-0abc123def456", "ready", []state.ModuleResource{
		{
			TypeName:   "AWS::EC2::SecurityGroup",
			Identifier: "sg-0a1b2c3d4e5f67890",
			Properties: map[string]string{
				"GroupName": "fabrica-horde-sg",
				"VpcId":     "vpc-0abc123",
			},
		},
		{
			TypeName:   "AWS::EC2::Instance",
			Identifier: "i-0abc123def456",
			Properties: map[string]string{
				"instanceType": "m7i.2xlarge",
				"volumeSize":   "100",
			},
		},
		{
			TypeName:   "AWS::IAM::Role",
			Identifier: "fabrica-horde-role",
			Properties: map[string]string{
				"RoleName": "fabrica-horde-role",
			},
		},
		{
			TypeName:   "AWS::IAM::InstanceProfile",
			Identifier: "fabrica-horde-profile",
			Properties: map[string]string{
				"InstanceProfileName": "fabrica-horde-profile",
			},
		},
	})
	return st
}

func testConfigWithHorde() *config.Config {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.Horde.AmiID = "ami-0abc123def456"
	cfg.Horde.InstanceType = "m7i.2xlarge"
	cfg.Horde.AllowedCIDR = "10.0.0.0/8"
	return cfg
}

func TestFormatValid(t *testing.T) {
	if !CloudFormation.Valid() {
		t.Error("CloudFormation should be valid")
	}
	if !Terraform.Valid() {
		t.Error("Terraform should be valid")
	}
	if Format("invalid").Valid() {
		t.Error("invalid format should not be valid")
	}
}

func TestNewGenerator(t *testing.T) {
	gen, err := NewGenerator(CloudFormation)
	if err != nil {
		t.Fatalf("unexpected error for CloudFormation: %v", err)
	}
	if gen == nil {
		t.Fatal("expected non-nil generator for CloudFormation")
	}

	gen, err = NewGenerator(Terraform)
	if err != nil {
		t.Fatalf("unexpected error for Terraform: %v", err)
	}
	if gen == nil {
		t.Fatal("expected non-nil generator for Terraform")
	}

	_, err = NewGenerator(Format("invalid"))
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
}

func TestSanitizeUserData(t *testing.T) {
	props := map[string]any{
		"UserData":     "base64encodedblob",
		"PasswordData": "secret",
		"InstanceType": "m7i.2xlarge",
		"NormalField":  "value",
	}
	result := sanitize(props)

	if result["UserData"] != "# REDACTED — cloud-init script (not included in export)" {
		t.Errorf("UserData not redacted: %v", result["UserData"])
	}
	if result["PasswordData"] != "# REDACTED" {
		t.Errorf("PasswordData not redacted: %v", result["PasswordData"])
	}
	if result["InstanceType"] != "m7i.2xlarge" {
		t.Errorf("InstanceType changed: %v", result["InstanceType"])
	}
	if result["NormalField"] != "value" {
		t.Errorf("NormalField changed: %v", result["NormalField"])
	}
}

func TestSanitizeBase64Blob(t *testing.T) {
	blob := strings.Repeat("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/", 10)
	props := map[string]any{
		"SecretField": blob,
		"NormalField": "short value",
	}
	result := sanitize(props)

	if result["SecretField"] != "# REDACTED — credential-like field" {
		t.Errorf("Long base64 blob not redacted: %v", result["SecretField"])
	}
	if result["NormalField"] != "short value" {
		t.Errorf("Normal field changed: %v", result["NormalField"])
	}
}

func TestLooksLikeBase64Blob(t *testing.T) {
	blob := strings.Repeat("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/", 10)
	if !looksLikeBase64Blob(blob) {
		t.Error("expected long base64 string to be detected")
	}
	if looksLikeBase64Blob("short") {
		t.Error("short string should not be detected")
	}
	if looksLikeBase64Blob("has spaces in it") {
		t.Error("string with spaces should not be detected")
	}
	// Long string with non-base64 character should return false
	longInvalid := strings.Repeat("ABC@DEF", 50)
	if looksLikeBase64Blob(longInvalid) {
		t.Error("long string with non-base64 chars should not be detected")
	}
	// Exactly 200 chars of valid base64 should return true
	exact200 := strings.Repeat("ABCDEFabcdef012345+/", 25)
	if len(exact200) >= 200 && !looksLikeBase64Blob(exact200) {
		t.Error("string >= 200 valid base64 chars should be detected")
	}
}

func TestToLogicalID(t *testing.T) {
	id := toLogicalID("horde", "AWS::EC2::Instance", "i-0abc123def456")
	if !strings.Contains(id, "HORDE") {
		t.Errorf("logical ID should contain module name: %s", id)
	}
	if !strings.Contains(id, "Instance") {
		t.Errorf("logical ID should contain type: %s", id)
	}
}

func TestTypeNameShort(t *testing.T) {
	if typeNameShort("AWS::EC2::Instance") != "Instance" {
		t.Errorf("unexpected short name: %s", typeNameShort("AWS::EC2::Instance"))
	}
	if typeNameShort("AWS::S3::Bucket") != "Bucket" {
		t.Errorf("unexpected short name: %s", typeNameShort("AWS::S3::Bucket"))
	}
	// Edge case: less than 3 parts
	if typeNameShort("EC2::Instance") != "Instance" {
		t.Errorf("unexpected short name for 2 parts: %s", typeNameShort("EC2::Instance"))
	}
}

func TestBuildModules(t *testing.T) {
	st := testStateWithHorde()
	cfg := testConfigWithHorde()

	modules, err := buildModules(st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have state-backend + horde
	if len(modules) != 2 {
		t.Fatalf("expected 2 modules, got %d", len(modules))
	}

	// First module should be state-backend
	if modules[0].Name != "state-backend" {
		t.Errorf("first module should be state-backend, got %s", modules[0].Name)
	}

	// Second module should be horde
	if modules[1].Name != "horde" {
		t.Errorf("second module should be horde, got %s", modules[1].Name)
	}

	// Horde should have 4 resources
	if len(modules[1].Resources) != 4 {
		t.Errorf("horde should have 4 resources, got %d", len(modules[1].Resources))
	}
}

func TestBuildModulesEmptyState(t *testing.T) {
	st := state.NewState("", "")
	cfg := config.Defaults()

	modules, err := buildModules(st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Empty state should produce no modules
	if len(modules) != 0 {
		t.Errorf("expected 0 modules for empty state, got %d", len(modules))
	}
}

func TestBuildModulesUnsupportedModule(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("ddc", "v1", "ready", []state.ModuleResource{
		{
			TypeName:   "AWS::EC2::Instance",
			Identifier: "i-ddc123",
			Properties: map[string]string{},
		},
	})
	cfg := config.Defaults()

	modules, err := buildModules(st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// DDC is not supported in V1, so only state-backend should be exported
	if len(modules) != 1 {
		t.Errorf("expected 1 module (state-backend only), got %d", len(modules))
	}
}

func TestGenerateOutputNilState(t *testing.T) {
	_, err := GenerateOutput(CloudFormation, nil, config.Defaults())
	if err == nil {
		t.Fatal("expected error for nil state")
	}
	if !strings.Contains(err.Error(), "no state available") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestGenerateOutputEmptyModules(t *testing.T) {
	st := state.NewState("", "")
	cfg := config.Defaults()

	_, err := GenerateOutput(CloudFormation, st, cfg)
	if err == nil {
		t.Fatal("expected error for empty modules")
	}
	if !strings.Contains(err.Error(), "no modules to export") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestCloudFormationGenerator(t *testing.T) {
	st := testStateWithHorde()
	cfg := testConfigWithHorde()

	data, err := GenerateOutput(CloudFormation, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Parse as YAML
	var tmpl map[string]any
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		t.Fatalf("invalid YAML output: %v\n%s", err, string(data))
	}

	// Check required fields
	if tmpl["AWSTemplateFormatVersion"] != "2010-09-09" {
		t.Errorf("missing AWSTemplateFormatVersion")
	}

	resources, ok := tmpl["Resources"].(map[string]any)
	if !ok {
		t.Fatal("missing Resources section")
	}

	// Should have state backend resources + horde resources
	if len(resources) < 6 {
		t.Errorf("expected at least 6 resources, got %d", len(resources))
	}

	// Check for state bucket
	if _, ok := resources["FabricaStateBucket"]; !ok {
		t.Error("missing FabricaStateBucket resource")
	}

	// Check for lock table
	if _, ok := resources["FabricaStateLockTable"]; !ok {
		t.Error("missing FabricaStateLockTable resource")
	}

	// Check that UserData is not present
	output := string(data)
	if strings.Contains(output, "base64encodedblob") {
		t.Error("UserData should be redacted from output")
	}
}

func TestTerraformGenerator(t *testing.T) {
	st := testStateWithHorde()
	cfg := testConfigWithHorde()

	data, err := GenerateOutput(Terraform, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := string(data)

	// Check for required resource blocks
	expectedResources := []string{
		`resource "aws_s3_bucket"`,
		`resource "aws_dynamodb_table"`,
		`resource "aws_security_group"`,
		`resource "aws_instance"`,
		`resource "aws_iam_role"`,
		`resource "aws_iam_instance_profile"`,
	}

	for _, expected := range expectedResources {
		if !strings.Contains(output, expected) {
			t.Errorf("missing resource block: %s\n%s", expected, output)
		}
	}

	// Check for terraform block
	if !strings.Contains(output, "terraform {") {
		t.Error("missing terraform block")
	}

	// Check for required_providers
	if !strings.Contains(output, "required_providers") {
		t.Error("missing required_providers block")
	}

	// Check that UserData is not present
	if strings.Contains(output, "base64encodedblob") {
		t.Error("UserData should be redacted from output")
	}
}

func TestStateBackendModule(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	cfg := config.Defaults()

	modules, err := buildModules(st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(modules) != 1 {
		t.Fatalf("expected 1 module, got %d", len(modules))
	}

	mod := modules[0]
	if mod.Name != "state-backend" {
		t.Errorf("expected state-backend module, got %s", mod.Name)
	}

	if len(mod.Resources) != 2 {
		t.Errorf("expected 2 resources, got %d", len(mod.Resources))
	}

	// Check S3 bucket
	bucket := mod.Resources[0]
	if bucket.TypeName != "AWS::S3::Bucket" {
		t.Errorf("expected S3 bucket, got %s", bucket.TypeName)
	}
	if bucket.LogicalID != "FabricaStateBucket" {
		t.Errorf("expected FabricaStateBucket, got %s", bucket.LogicalID)
	}

	// Check DynamoDB table
	table := mod.Resources[1]
	if table.TypeName != "AWS::DynamoDB::Table" {
		t.Errorf("expected DynamoDB table, got %s", table.TypeName)
	}
	if table.LogicalID != "FabricaStateLockTable" {
		t.Errorf("expected FabricaStateLockTable, got %s", table.LogicalID)
	}
}

func TestInstanceTypeForModule(t *testing.T) {
	cfg := testConfigWithHorde()

	if instanceTypeForModule("horde", cfg) != "m7i.2xlarge" {
		t.Errorf("unexpected horde instance type: %s", instanceTypeForModule("horde", cfg))
	}

	cfg.Perforce.InstanceType = "c5.2xlarge"
	if instanceTypeForModule("perforce", cfg) != "c5.2xlarge" {
		t.Errorf("unexpected perforce instance type: %s", instanceTypeForModule("perforce", cfg))
	}

	// Default for perforce when not set
	cfg2 := config.Defaults()
	if instanceTypeForModule("perforce", cfg2) != "c5.2xlarge" {
		t.Errorf("unexpected default perforce instance type: %s", instanceTypeForModule("perforce", cfg2))
	}

	// Default for lore
	if instanceTypeForModule("lore", cfg2) != "m5.xlarge" {
		t.Errorf("unexpected default lore instance type: %s", instanceTypeForModule("lore", cfg2))
	}

	// Unknown module returns empty string
	if instanceTypeForModule("unknown", cfg) != "" {
		t.Errorf("expected empty string for unknown module, got: %s", instanceTypeForModule("unknown", cfg))
	}

	// Nil config returns empty string
	if instanceTypeForModule("horde", nil) != "" {
		t.Errorf("expected empty string for nil config, got: %s", instanceTypeForModule("horde", nil))
	}
}

func TestSGNameForModule(t *testing.T) {
	if sgNameForModule("horde") != "fabrica-horde-sg" {
		t.Errorf("unexpected SG name: %s", sgNameForModule("horde"))
	}
	if sgNameForModule("perforce") != "fabrica-perforce-sg" {
		t.Errorf("unexpected SG name: %s", sgNameForModule("perforce"))
	}
}

func TestRoleNameForModule(t *testing.T) {
	if roleNameForModule("horde") != "fabrica-horde-role" {
		t.Errorf("unexpected role name: %s", roleNameForModule("horde"))
	}
}

func TestProfileNameForModule(t *testing.T) {
	if profileNameForModule("horde") != "fabrica-horde-profile" {
		t.Errorf("unexpected profile name: %s", profileNameForModule("horde"))
	}
}

func TestDefaultAssumeRolePolicy(t *testing.T) {
	policy := defaultAssumeRolePolicy()
	if policy["Version"] != "2012-10-17" {
		t.Errorf("unexpected policy version: %v", policy["Version"])
	}
}

func TestDefaultTags(t *testing.T) {
	tags := defaultTags("horde")
	if len(tags) != 3 {
		t.Errorf("expected 3 tags, got %d", len(tags))
	}
	found := false
	for _, tag := range tags {
		if tag["Key"] == "ManagedBy" && tag["Value"] == "fabrica" {
			found = true
		}
	}
	if !found {
		t.Error("missing ManagedBy tag")
	}
}

func TestModuleNames(t *testing.T) {
	modules := []ExportModule{
		{Name: "state-backend"},
		{Name: "horde"},
	}
	names := moduleNames(modules)
	if names != "state-backend, horde" {
		t.Errorf("unexpected module names: %s", names)
	}
}

func TestExtractPropertiesEC2Instance(t *testing.T) {
	res := state.ModuleResource{
		TypeName:   "AWS::EC2::Instance",
		Identifier: "i-123",
		Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
			"volumeSize":   "100",
		},
	}
	cfg := testConfigWithHorde()
	props := extractProperties("horde", res, cfg)

	if props["InstanceType"] != "m7i.2xlarge" {
		t.Errorf("unexpected instance type: %v", props["InstanceType"])
	}
	if props["ImageId"] != "ami-0abc123def456" {
		t.Errorf("unexpected AMI ID: %v", props["ImageId"])
	}
	if props["MetadataOptions"] == nil {
		t.Error("missing MetadataOptions")
	}
	// volumeSize should be mapped to BlockDeviceMappings, not leaked as a raw property
	if _, ok := props["volumeSize"]; ok {
		t.Error("volumeSize should not appear as a top-level property")
	}
	if _, ok := props["__volumeSize"]; ok {
		t.Error("__volumeSize internal key should be cleaned up")
	}
	if bdm, ok := props["BlockDeviceMappings"].([]map[string]any); !ok || len(bdm) == 0 {
		t.Error("expected BlockDeviceMappings from volumeSize")
	}
}

func TestExtractPropertiesSecurityGroup(t *testing.T) {
	res := state.ModuleResource{
		TypeName:   "AWS::EC2::SecurityGroup",
		Identifier: "sg-123",
		Properties: map[string]string{},
	}
	cfg := testConfigWithHorde()
	props := extractProperties("horde", res, cfg)

	if props["GroupName"] != "fabrica-horde-sg" {
		t.Errorf("unexpected group name: %v", props["GroupName"])
	}
	if props["SecurityGroupIngress"] == nil {
		t.Error("missing SecurityGroupIngress")
	}
}

func TestExtractPropertiesIAMRole(t *testing.T) {
	res := state.ModuleResource{
		TypeName:   "AWS::IAM::Role",
		Identifier: "fabrica-horde-role",
		Properties: map[string]string{},
	}
	cfg := testConfigWithHorde()
	props := extractProperties("horde", res, cfg)

	if props["RoleName"] != "fabrica-horde-role" {
		t.Errorf("unexpected role name: %v", props["RoleName"])
	}
	if props["AssumeRolePolicyDocument"] == nil {
		t.Error("missing AssumeRolePolicyDocument")
	}
}

func TestExtractPropertiesInstanceProfile(t *testing.T) {
	res := state.ModuleResource{
		TypeName:   "AWS::IAM::InstanceProfile",
		Identifier: "fabrica-horde-profile",
		Properties: map[string]string{},
	}
	cfg := testConfigWithHorde()
	props := extractProperties("horde", res, cfg)

	if props["InstanceProfileName"] != "fabrica-horde-profile" {
		t.Errorf("unexpected profile name: %v", props["InstanceProfileName"])
	}
}

func TestExtractPropertiesNilConfig(t *testing.T) {
	res := state.ModuleResource{
		TypeName:   "AWS::EC2::Instance",
		Identifier: "i-123",
		Properties: map[string]string{},
	}
	props := extractProperties("horde", res, nil)

	// Should not crash with nil config
	if props == nil {
		t.Fatal("expected non-nil properties")
	}
}

func TestSGRulesForModule(t *testing.T) {
	cfg := testConfigWithHorde()
	rules := sgRulesForModule("horde", cfg)

	if len(rules) != 2 {
		t.Errorf("expected 2 rules for horde, got %d", len(rules))
	}

	rules = sgRulesForModule("perforce", cfg)
	if len(rules) != 1 {
		t.Errorf("expected 1 rule for perforce, got %d", len(rules))
	}

	rules = sgRulesForModule("lore", cfg)
	if len(rules) != 3 {
		t.Errorf("expected 3 rules for lore, got %d", len(rules))
	}
}

func TestBuildPerforceModule(t *testing.T) {
	ms := state.ModuleState{
		Name:   "perforce",
		Status: "ready",
		Resources: []state.ModuleResource{
			{
				TypeName:   "AWS::EC2::SecurityGroup",
				Identifier: "sg-p4",
				Properties: map[string]string{},
			},
			{
				TypeName:   "AWS::EC2::Instance",
				Identifier: "i-p4",
				Properties: map[string]string{
					"instanceType": "c5.2xlarge",
					"volumeSize":   "500",
				},
			},
		},
	}
	cfg := config.Defaults()
	cfg.Perforce.InstanceType = "c5.2xlarge"
	cfg.Perforce.AllowedCIDR = "10.0.0.0/8"

	mod := buildPerforceModule(ms, cfg)
	if mod.Name != "perforce" {
		t.Errorf("unexpected module name: %s", mod.Name)
	}
	if len(mod.Resources) != 2 {
		t.Errorf("expected 2 resources, got %d", len(mod.Resources))
	}
}

func TestBuildLoreModule(t *testing.T) {
	ms := state.ModuleState{
		Name:   "lore",
		Status: "ready",
		Resources: []state.ModuleResource{
			{
				TypeName:   "AWS::EC2::SecurityGroup",
				Identifier: "sg-lore",
				Properties: map[string]string{},
			},
			{
				TypeName:   "AWS::EC2::Instance",
				Identifier: "i-lore",
				Properties: map[string]string{
					"instanceType": "m5.xlarge",
					"volumeSize":   "500",
				},
			},
		},
	}
	cfg := config.Defaults()
	cfg.Lore.AmiID = "ami-lore123"
	cfg.Lore.InstanceType = "m5.xlarge"
	cfg.Lore.AllowedCIDR = "10.0.0.0/8"

	mod := buildLoreModule(ms, cfg)
	if mod.Name != "lore" {
		t.Errorf("unexpected module name: %s", mod.Name)
	}
	if len(mod.Resources) != 2 {
		t.Errorf("expected 2 resources, got %d", len(mod.Resources))
	}
}

func TestTerraformResourceType(t *testing.T) {
	gen := &terraformGenerator{}

	tests := []struct {
		input    string
		expected string
	}{
		{"AWS::EC2::Instance", "aws_instance"},
		{"AWS::EC2::SecurityGroup", "aws_security_group"},
		{"AWS::S3::Bucket", "aws_s3_bucket"},
		{"AWS::DynamoDB::Table", "aws_dynamodb_table"},
		{"AWS::IAM::Role", "aws_iam_role"},
		{"AWS::IAM::InstanceProfile", "aws_iam_instance_profile"},
	}

	for _, tc := range tests {
		got := gen.tfResourceType(tc.input)
		if got != tc.expected {
			t.Errorf("tfResourceType(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestTerraformAttributeName(t *testing.T) {
	gen := &terraformGenerator{}

	tests := []struct {
		input    string
		expected string
	}{
		{"BucketName", "bucket"},
		{"InstanceType", "instance_type"},
		{"ImageId", "ami"},
		{"TableName", "name"},
		{"VpcId", "vpc_id"},
	}

	for _, tc := range tests {
		got := gen.tfAttributeName(tc.input)
		if got != tc.expected {
			t.Errorf("tfAttributeName(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestCloudFormationNoUserDataInOutput(t *testing.T) {
	st := testStateWithHorde()
	cfg := testConfigWithHorde()

	// Add a UserData property to the instance
	for i := range st.Modules[0].Resources {
		if st.Modules[0].Resources[i].TypeName == "AWS::EC2::Instance" {
			st.Modules[0].Resources[i].Properties["UserData"] = strings.Repeat("A", 300)
		}
	}

	data, err := GenerateOutput(CloudFormation, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := string(data)
	if strings.Contains(output, strings.Repeat("A", 300)) {
		t.Error("base64 blob should not appear in CloudFormation output")
	}
}

func TestTerraformNoUserDataInOutput(t *testing.T) {
	st := testStateWithHorde()
	cfg := testConfigWithHorde()

	// Add a UserData property to the instance
	for i := range st.Modules[0].Resources {
		if st.Modules[0].Resources[i].TypeName == "AWS::EC2::Instance" {
			st.Modules[0].Resources[i].Properties["UserData"] = strings.Repeat("B", 300)
		}
	}

	data, err := GenerateOutput(Terraform, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := string(data)
	if strings.Contains(output, strings.Repeat("B", 300)) {
		t.Error("base64 blob should not appear in Terraform output")
	}
}

func TestCloudFormationOutputs(t *testing.T) {
	st := testStateWithHorde()
	cfg := testConfigWithHorde()

	data, err := GenerateOutput(CloudFormation, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var tmpl map[string]any
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}

	outputs, ok := tmpl["Outputs"].(map[string]any)
	if !ok {
		t.Fatal("missing Outputs section")
	}

	// Should have outputs for state bucket and lock table
	if _, ok := outputs["StateBucketName"]; !ok {
		t.Error("missing StateBucketName output")
	}
	if _, ok := outputs["StateLockTableName"]; !ok {
		t.Error("missing StateLockTableName output")
	}
}

func TestTerraformOutputs(t *testing.T) {
	st := testStateWithHorde()
	cfg := testConfigWithHorde()

	data, err := GenerateOutput(Terraform, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := string(data)
	if !strings.Contains(output, "output \"state_bucket_name\"") {
		t.Error("missing state_bucket_name output")
	}
	if !strings.Contains(output, "output \"state_lock_table_name\"") {
		t.Error("missing state_lock_table_name output")
	}
}

func TestCloudFormationDependsOn(t *testing.T) {
	st := testStateWithHorde()
	cfg := testConfigWithHorde()

	data, err := GenerateOutput(CloudFormation, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var tmpl map[string]any
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}

	resources := tmpl["Resources"].(map[string]any)

	// Find the instance resource and check DependsOn
	for name, res := range resources {
		rm := res.(map[string]any)
		if rm["Type"] == "AWS::EC2::Instance" {
			deps, ok := rm["DependsOn"]
			if !ok {
				t.Errorf("instance %s should have DependsOn", name)
			} else {
				t.Logf("instance %s depends on: %v", name, deps)
			}
		}
	}
}

func TestValidFormat(t *testing.T) {
	if !ValidFormat("cloudformation") {
		t.Error("cloudformation should be valid")
	}
	if !ValidFormat("terraform") {
		t.Error("terraform should be valid")
	}
	if ValidFormat("invalid") {
		t.Error("invalid should not be valid")
	}
}

// TestVolumeSizeNotLeaked verifies that volumeSize from state is mapped to
// BlockDeviceMappings and does not appear as a raw top-level property in output.
// Regression test for: volumeSize was leaking as an invalid EC2 property.
func TestVolumeSizeNotLeaked(t *testing.T) {
	st := testStateWithHorde()
	cfg := testConfigWithHorde()

	// CloudFormation: volumeSize should not appear as a raw property
	cfData, err := GenerateOutput(CloudFormation, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cfOutput := string(cfData)
	if strings.Contains(cfOutput, "volumeSize") {
		t.Error("volumeSize should not appear as a raw CloudFormation property")
	}
	// BlockDeviceMappings should be present instead
	if !strings.Contains(cfOutput, "BlockDeviceMappings") {
		t.Error("BlockDeviceMappings should be present in CloudFormation output")
	}

	// Terraform: volume_size should only appear inside ebs_block_device,
	// not as a top-level aws_instance property. Check that the raw
	// camelCase volumeSize key does not appear.
	tfData, err := GenerateOutput(Terraform, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tfOutput := string(tfData)
	if strings.Contains(tfOutput, "volumeSize") {
		t.Error("raw camelCase volumeSize should not appear in Terraform output")
	}
	// ebs_block_device should be present (volume mapped correctly)
	if !strings.Contains(tfOutput, "ebs_block_device") {
		t.Error("ebs_block_device should be present in Terraform output")
	}
}

// TestTerraformOutputReferences verifies that Terraform output references include
// the resource type prefix (e.g., ${aws_instance.horde_instance_i.id}).
// Regression test for: outputs used ${horde_instance_i.id} without resource type.
func TestTerraformOutputReferences(t *testing.T) {
	st := testStateWithHorde()
	cfg := testConfigWithHorde()

	data, err := GenerateOutput(Terraform, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	output := string(data)

	// Instance output should reference aws_instance.<name>.id
	if !strings.Contains(output, "aws_instance.") {
		t.Error("Terraform output should reference aws_instance resource type")
	}
	// Security group output should reference aws_security_group.<name>.id
	if !strings.Contains(output, "aws_security_group.") {
		t.Error("Terraform output should reference aws_security_group resource type")
	}
	// IAM role output should reference aws_iam_role.<name>.arn
	if !strings.Contains(output, "aws_iam_role.") {
		t.Error("Terraform output should reference aws_iam_role resource type")
	}
}

// TestExtractPropertiesCamelCase verifies that extractProperties correctly
// handles camelCase keys from production state (instanceType, volumeSize).
func TestExtractPropertiesCamelCase(t *testing.T) {
	res := state.ModuleResource{
		TypeName:   "AWS::EC2::Instance",
		Identifier: "i-test",
		Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
			"volumeSize":   "100",
		},
	}
	cfg := testConfigWithHorde()
	props := extractProperties("horde", res, cfg)

	// instanceType should be normalized to InstanceType
	if props["InstanceType"] != "m7i.2xlarge" {
		t.Errorf("InstanceType not set from camelCase: %v", props["InstanceType"])
	}
	// volumeSize should NOT appear as a raw property
	if _, ok := props["volumeSize"]; ok {
		t.Error("volumeSize should not appear as a top-level property")
	}
	// BlockDeviceMappings should be built from volumeSize
	if _, ok := props["BlockDeviceMappings"]; !ok {
		t.Error("BlockDeviceMappings should be built from volumeSize")
	}
}

// TestLooksLikeBase64BlobEdgeCases covers edge cases in base64 detection.
func TestLooksLikeBase64BlobEdgeCases(t *testing.T) {
	// Long string with invalid characters should return false
	longInvalid := strings.Repeat("A!B", 100)
	if looksLikeBase64Blob(longInvalid) {
		t.Error("long string with invalid chars should not be detected as base64")
	}
	// Exactly 200 chars of valid base64 should return true
	exact200 := strings.Repeat("A", 200)
	if !looksLikeBase64Blob(exact200) {
		t.Error("exactly 200 valid chars should be detected as base64")
	}
	// 199 chars should return false (below threshold)
	below200 := strings.Repeat("A", 199)
	if looksLikeBase64Blob(below200) {
		t.Error("199 chars should not be detected as base64")
	}
	// Long string with = padding should return true
	withPadding := strings.Repeat("A", 198) + "=="
	if !looksLikeBase64Blob(withPadding) {
		t.Error("long string with = padding should be detected as base64")
	}
	// Long string with / should return true
	withSlash := strings.Repeat("A/", 100)
	if !looksLikeBase64Blob(withSlash) {
		t.Error("long string with / should be detected as base64")
	}
}

// TestTypeNameShortEdgeCases covers edge cases in type name shortening.
func TestTypeNameShortEdgeCases(t *testing.T) {
	// Standard 3-part type
	if typeNameShort("AWS::EC2::Instance") != "Instance" {
		t.Errorf("unexpected: %s", typeNameShort("AWS::EC2::Instance"))
	}
	// Two-part type (falls back to last part)
	if typeNameShort("AWS::EC2") != "EC2" {
		t.Errorf("unexpected: %s", typeNameShort("AWS::EC2"))
	}
	// Single-part type
	if typeNameShort("Instance") != "Instance" {
		t.Errorf("unexpected: %s", typeNameShort("Instance"))
	}
}

// TestBuildModulesUnsupportedModuleDefault covers the default: continue branch.
func TestBuildModulesUnsupportedModuleDefault(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("workstation", "v1", "ready", []state.ModuleResource{
		{
			TypeName:   "AWS::EC2::Instance",
			Identifier: "i-ws1",
			Properties: map[string]string{},
		},
	})
	st.UpsertModule("ci", "v1", "ready", []state.ModuleResource{
		{
			TypeName:   "AWS::IAM::Role",
			Identifier: "ci-role",
			Properties: map[string]string{},
		},
	})
	st.UpsertModule("deploy", "v1", "ready", []state.ModuleResource{
		{
			TypeName:   "AWS::GameLift::Fleet",
			Identifier: "fleet-1",
			Properties: map[string]string{},
		},
	})
	cfg := config.Defaults()

	modules, err := buildModules(st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only state-backend should be exported; workstation, ci, deploy are skipped
	if len(modules) != 1 {
		t.Errorf("expected 1 module (state-backend), got %d", len(modules))
	}
	if modules[0].Name != "state-backend" {
		t.Errorf("expected state-backend, got %s", modules[0].Name)
	}
}

// TestTerraformResourceTypeDefault covers the generic conversion path.
func TestTerraformResourceTypeDefault(t *testing.T) {
	gen := &terraformGenerator{}
	// Unknown type — generic conversion
	got := gen.tfResourceType("AWS::Lambda::Function")
	if got != "aws_lambda_function" {
		t.Errorf("tfResourceType(AWS::Lambda::Function) = %q, want aws_lambda_function", got)
	}
	// Unknown type with fewer parts
	got2 := gen.tfResourceType("Lambda::Function")
	if got2 != "aws_function" {
		t.Errorf("tfResourceType(Lambda::Function) = %q, want aws_function", got2)
	}
}

// TestTerraformHclEdgeCases covers type assertion failures in HCL helpers.
func TestTerraformHclEdgeCases(t *testing.T) {
	gen := &terraformGenerator{}
	// hclTags with wrong type
	if gen.hclTags("not a slice") != "" {
		t.Error("hclTags should return empty for wrong type")
	}
	// hclSGIngress with wrong type
	if gen.hclSGIngress("not a slice") != "" {
		t.Error("hclSGIngress should return empty for wrong type")
	}
	// hclPolicyDoc with wrong type
	if gen.hclPolicyDoc("not a map") != "" {
		t.Error("hclPolicyDoc should return empty for wrong type")
	}
	// hclPolicyArns with string slice
	arns := []string{"arn:aws:iam:://policy1"}
	out := gen.hclPolicyArns(arns)
	if !strings.Contains(out, "policy1") {
		t.Error("hclPolicyArns should handle string slice")
	}
	// hclPolicyArns with wrong type
	if gen.hclPolicyArns("not a slice") != "" {
		t.Error("hclPolicyArns should return empty for wrong type")
	}
	// hclBlockDevices with wrong type
	if gen.hclBlockDevices("not a slice") != "" {
		t.Error("hclBlockDevices should return empty for wrong type")
	}
	// hclKeySchema with wrong type
	if gen.hclKeySchema("not a slice") != "" {
		t.Error("hclKeySchema should return empty for wrong type")
	}
	// hclAttrDefs with wrong type
	if gen.hclAttrDefs("not a slice") != "" {
		t.Error("hclAttrDefs should return empty for wrong type")
	}
	// hclVersioning with wrong type
	if gen.hclVersioning("not a map") != "" {
		t.Error("hclVersioning should return empty for wrong type")
	}
	// hclEncryption with wrong type
	if gen.hclEncryption("not a map") != "" {
		t.Error("hclEncryption should return empty for wrong type")
	}
	// hclPublicAccess with wrong type
	if gen.hclPublicAccess("not a map") != "" {
		t.Error("hclPublicAccess should return empty for wrong type")
	}
	// hclMetadataOptions with wrong type
	if gen.hclMetadataOptions("not a map") != "" {
		t.Error("hclMetadataOptions should return empty for wrong type")
	}
	// hclRoles with wrong type
	if gen.hclRoles("not a slice") != "" {
		t.Error("hclRoles should return empty for wrong type")
	}
}

// TestTerraformHclScalar covers all scalar type cases.
func TestTerraformHclScalar(t *testing.T) {
	gen := &terraformGenerator{}
	// String
	out := gen.hclScalar("Name", "test")
	if !strings.Contains(out, "test") {
		t.Error("hclScalar should handle string")
	}
	// Int
	out = gen.hclScalar("Port", 8080)
	if !strings.Contains(out, "8080") {
		t.Error("hclScalar should handle int")
	}
	// Int64
	out = gen.hclScalar("Size", int64(100))
	if !strings.Contains(out, "100") {
		t.Error("hclScalar should handle int64")
	}
	// Float64
	out = gen.hclScalar("Ratio", float64(3.14))
	if !strings.Contains(out, "3") {
		t.Error("hclScalar should handle float64")
	}
	// Bool
	out = gen.hclScalar("Enabled", true)
	if !strings.Contains(out, "true") {
		t.Error("hclScalar should handle bool")
	}
	// Default (complex type)
	out = gen.hclScalar("Complex", map[string]any{"a": 1})
	if !strings.Contains(out, "#") {
		t.Error("hclScalar should comment out complex types")
	}
}

// TestTerraformAttributeNameDefault covers the snake_case conversion fallback.
func TestTerraformAttributeNameDefault(t *testing.T) {
	gen := &terraformGenerator{}
	got := gen.tfAttributeName("SomeUnknownAttr")
	if got != "some_unknown_attr" {
		t.Errorf("tfAttributeName(SomeUnknownAttr) = %q, want some_unknown_attr", got)
	}
}

// TestCloudFormationIngressEdgeCases covers cfIngressRules edge cases.
func TestCloudFormationIngressEdgeCases(t *testing.T) {
	// Nil rules list
	out := cfIngressRules(nil)
	if out != nil {
		t.Error("cfIngressRules(nil) should return nil")
	}
	// Empty slice — returns empty slice, not nil
	out = cfIngressRules([]map[string]any{})
	if len(out) != 0 {
		t.Error("cfIngressRules([]) should return empty slice")
	}
}

// TestCloudFormationTagsEdgeCases covers cfTags edge cases.
func TestCloudFormationTagsEdgeCases(t *testing.T) {
	// Nil tags
	out := cfTags(nil)
	if out != nil {
		t.Error("cfTags(nil) should return nil")
	}
	// Empty slice
	out = cfTags([]map[string]string{})
	if out == nil || len(out) != 0 {
		t.Error("cfTags([]) should return empty slice")
	}
}

// TestCloudFormationBlockDevicesEdgeCases covers cfBlockDevices edge cases.
func TestCloudFormationBlockDevicesEdgeCases(t *testing.T) {
	// Nil devices
	out := cfBlockDevices(nil)
	if out != nil {
		t.Error("cfBlockDevices(nil) should return nil")
	}
	// Device without EBS — returns slice with nil entry
	devs := []map[string]any{{"DeviceName": "/dev/sda1"}}
	out = cfBlockDevices(devs)
	if out == nil || len(out) != 1 {
		t.Error("cfBlockDevices should return slice for device without EBS")
	}
	// Device with EBS but no DeleteOnTermination
	devs2 := []map[string]any{{"DeviceName": "/dev/sda1", "Ebs": map[string]any{"VolumeSize": float64(100)}}}
	out = cfBlockDevices(devs2)
	if out == nil || len(out) != 1 {
		t.Error("cfBlockDevices should handle EBS without DeleteOnTermination")
	}
}

// TestSgRulesForModuleDefault covers the default case in sgRulesForModule.
func TestSgRulesForModuleDefault(t *testing.T) {
	cfg := config.Defaults()
	rules := sgRulesForModule("unknown", cfg)
	if len(rules) != 0 {
		t.Errorf("expected 0 rules for unknown module, got %d", len(rules))
	}
}

// TestGenerateOutputErrorPaths covers error paths in GenerateOutput.
func TestGenerateOutputErrorPaths(t *testing.T) {
	// Invalid format should error at NewGenerator
	_, err := GenerateOutput(Format("invalid"), testStateWithHorde(), testConfigWithHorde())
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
	// The error comes from NewGenerator rejecting the invalid format
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestFindResourceIDEdgeCases covers findResourceID edge cases.
func TestFindResourceIDEdgeCases(t *testing.T) {
	modules := []ExportModule{
		{
			Name:   "horde",
			Status: "ready",
			Resources: []ExportResource{
				{TypeName: "AWS::EC2::Instance", LogicalID: "HordeInstanceI"},
			},
		},
	}
	// Find existing (empty module matches all)
	id := findResourceID(modules, "", "AWS::EC2::Instance")
	if id != "HordeInstanceI" {
		t.Errorf("expected HordeInstanceI, got %s", id)
	}
	// Find with specific module
	id = findResourceID(modules, "horde", "AWS::EC2::Instance")
	if id != "HordeInstanceI" {
		t.Errorf("expected HordeInstanceI with module filter, got %s", id)
	}
	// Find non-existing
	id = findResourceID(modules, "", "AWS::S3::Bucket")
	if id != "" {
		t.Errorf("expected empty string for non-existent, got %s", id)
	}
	// Module filter that doesn't match
	id = findResourceID(modules, "perforce", "AWS::EC2::Instance")
	if id != "" {
		t.Errorf("expected empty string for non-matching module, got %s", id)
	}
}

// TestHclRolesWithSlice covers hclRoles with valid input.
func TestHclRolesWithSlice(t *testing.T) {
	gen := &terraformGenerator{}
	out := gen.hclRoles([]string{"MyRole"})
	if !strings.Contains(out, "MyRole") {
		t.Error("hclRoles should output role name")
	}
}

// TestHclKeySchemaWithValid covers hclKeySchema with valid input.
func TestHclKeySchemaWithValid(t *testing.T) {
	gen := &terraformGenerator{}
	ks := []map[string]any{{"AttributeName": "UserId"}}
	out := gen.hclKeySchema(ks)
	if !strings.Contains(out, "UserId") {
		t.Error("hclKeySchema should output attribute name")
	}
}

// TestHclVersioningWithValid covers hclVersioning with valid input.
func TestHclVersioningWithValid(t *testing.T) {
	gen := &terraformGenerator{}
	vc := map[string]any{"Status": "Enabled"}
	out := gen.hclVersioning(vc)
	if !strings.Contains(out, "enabled") {
		t.Error("hclVersioning should output enabled")
	}
}

// TestHclPublicAccessWithValid covers hclPublicAccess with valid input.
func TestHclPublicAccessWithValid(t *testing.T) {
	gen := &terraformGenerator{}
	out := gen.hclPublicAccess(map[string]any{})
	if !strings.Contains(out, "Public access block") {
		t.Error("hclPublicAccess should output comment")
	}
}

// TestHclMetadataOptionsWithValid covers hclMetadataOptions with valid input.
func TestHclMetadataOptionsWithValid(t *testing.T) {
	gen := &terraformGenerator{}
	mo := map[string]any{"HttpTokens": "required"}
	out := gen.hclMetadataOptions(mo)
	if !strings.Contains(out, "required") {
		t.Error("hclMetadataOptions should output http_tokens")
	}
}

// TestHclEncryptionWithValid covers hclEncryption with valid input.
func TestHclEncryptionWithValid(t *testing.T) {
	gen := &terraformGenerator{}
	be := map[string]any{
		"ServerSideEncryptionConfiguration": []map[string]any{
			{
				"ServerSideEncryptionByDefault": map[string]any{
					"SSEAlgorithm": "AES256",
				},
			},
		},
	}
	out := gen.hclEncryption(be)
	if !strings.Contains(out, "AES256") {
		t.Error("hclEncryption should output sse_algorithm")
	}
}

// TestHclAttrDefsWithValid covers hclAttrDefs with valid input.
func TestHclAttrDefsWithValid(t *testing.T) {
	gen := &terraformGenerator{}
	ad := []map[string]any{{"AttributeName": "UserId", "AttributeType": "S"}}
	out := gen.hclAttrDefs(ad)
	if !strings.Contains(out, "UserId") {
		t.Error("hclAttrDefs should output attribute name")
	}
	if !strings.Contains(out, "S") {
		t.Error("hclAttrDefs should output attribute type")
	}
}

// TestHclBlockDevicesWithValid covers hclBlockDevices with valid input.
func TestHclBlockDevicesWithValid(t *testing.T) {
	gen := &terraformGenerator{}
	devs := []map[string]any{
		{
			"DeviceName": "/dev/sda1",
			"Ebs": map[string]any{
				"VolumeSize":          float64(100),
				"VolumeType":          "gp3",
				"DeleteOnTermination": true,
			},
		},
	}
	out := gen.hclBlockDevices(devs)
	if !strings.Contains(out, "/dev/sda1") {
		t.Error("hclBlockDevices should output device name")
	}
	if !strings.Contains(out, "gp3") {
		t.Error("hclBlockDevices should output volume type")
	}
}

// TestHclSGIngressWithValid covers hclSGIngress with valid input including description.
func TestHclSGIngressWithValid(t *testing.T) {
	gen := &terraformGenerator{}
	rules := []map[string]any{
		{
			"IpProtocol":  "-1",
			"FromPort":    float64(0),
			"ToPort":      float64(65535),
			"CidrIp":      "10.0.0.0/8",
			"Description": "Allow all internal traffic",
		},
	}
	out := gen.hclSGIngress(rules)
	if !strings.Contains(out, "Allow all internal traffic") {
		t.Error("hclSGIngress should output description when present")
	}
}

// TestHclTagsWithValid covers hclTags with valid input.
func TestHclTagsWithValid(t *testing.T) {
	gen := &terraformGenerator{}
	tags := []map[string]string{{"Key": "Name", "Value": "test"}}
	out := gen.hclTags(tags)
	if !strings.Contains(out, "Name") {
		t.Error("hclTags should output tag key")
	}
	if !strings.Contains(out, "test") {
		t.Error("hclTags should output tag value")
	}
}

// TestHclPolicyDocWithValid covers hclPolicyDoc with valid input.
func TestHclPolicyDocWithValid(t *testing.T) {
	gen := &terraformGenerator{}
	doc := map[string]any{"Version": "2012-10-17"}
	out := gen.hclPolicyDoc(doc)
	if !strings.Contains(out, "2012-10-17") {
		t.Error("hclPolicyDoc should output version")
	}
	if !strings.Contains(out, "ec2.amazonaws.com") {
		t.Error("hclPolicyDoc should output service")
	}
}

// TestHclPolicyArnsWithMap covers hclPolicyArns with map slice input.
func TestHclPolicyArnsWithMap(t *testing.T) {
	gen := &terraformGenerator{}
	arns := []map[string]any{{"arn": "arn:aws:iam:://policy1"}}
	out := gen.hclPolicyArns(arns)
	if !strings.Contains(out, "policy1") {
		t.Error("hclPolicyArns should output arn from map")
	}
}

// TestResourceToHclErrorPath covers the error return path in resourceToHCL.
func TestResourceToHclErrorPath(t *testing.T) {
	gen := &terraformGenerator{}
	// Use an unknown type that will hit the default case in tfResourceType
	res := ExportResource{
		Module:     "test",
		TypeName:   "AWS::Unknown::Resource",
		LogicalID:  "TestResource",
		Properties: map[string]any{},
	}
	block, err := gen.resourceToHCL(res)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(block, "resource") {
		t.Error("resourceToHCL should produce a resource block for unknown types")
	}
}
