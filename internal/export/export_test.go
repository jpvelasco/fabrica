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

	// DDC is now supported in V2 — state-backend + ddc should be exported
	if len(modules) != 2 {
		t.Errorf("expected 2 modules (state-backend + ddc), got %d", len(modules))
	}
	if len(modules) >= 2 {
		if modules[1].Name != "ddc" {
			t.Errorf("expected ddc module, got %s", modules[1].Name)
		}
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
	// CI uses fabrica-ci-codebuild (not fabrica-ci-role)
	if roleNameForModule("ci") != "fabrica-ci-codebuild" {
		t.Errorf("unexpected CI role name: %s", roleNameForModule("ci"))
	}
	// Deploy uses fabrica-deploy-gamelift (not fabrica-deploy-role)
	if roleNameForModule("deploy") != "fabrica-deploy-gamelift" {
		t.Errorf("unexpected Deploy role name: %s", roleNameForModule("deploy"))
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

func TestBuildPerforceModuleWithImageId(t *testing.T) {
	// Perforce with imageId in Properties should export the real AMI.
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
					"instanceType": "m5.xlarge",
					"volumeSize":   "500",
					"imageId":      "ami-resolved123",
				},
			},
		},
	}
	cfg := config.Defaults()

	mod := buildPerforceModule(ms, cfg)
	if mod.Name != "perforce" {
		t.Errorf("unexpected module name: %s", mod.Name)
	}

	// Find the instance resource and verify ImageId is the resolved AMI.
	var found bool
	for _, r := range mod.Resources {
		if r.TypeName == "AWS::EC2::Instance" {
			found = true
			if imgID, ok := r.Properties["ImageId"]; !ok || imgID != "ami-resolved123" {
				t.Errorf("Instance ImageId = %v, want ami-resolved123", imgID)
			}
		}
	}
	if !found {
		t.Error("AWS::EC2::Instance not found in export resources")
	}
}

func TestExtractPropertiesImageIdMapping(t *testing.T) {
	// Verify imageId → ImageId normalization in extractProperties.
	r := state.ModuleResource{
		TypeName:   "AWS::EC2::Instance",
		Identifier: "i-test",
		Properties: map[string]string{
			"instanceType": "m5.xlarge",
			"imageId":      "ami-abc",
			"volumeSize":   "100",
		},
	}
	cfg := config.Defaults()
	props := extractProperties("perforce", r, cfg)

	if props["ImageId"] != "ami-abc" {
		t.Errorf("ImageId = %v, want ami-abc", props["ImageId"])
	}
	// The lowercase key should not appear.
	if _, ok := props["imageId"]; ok {
		t.Error("lowercase imageId should not appear in output")
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
// the resource type prefix (e.g., aws_instance.horde_instance_i.id) without
// HCL1-style ${} interpolation.
// Regression test for: outputs used ${horde_instance_i.id} without resource type,
// and ${resource.attr} which is invalid HCL2.
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
	// State bucket output should reference aws_s3_bucket.<name>.id
	if !strings.Contains(output, "aws_s3_bucket.") {
		t.Error("Terraform output should reference aws_s3_bucket resource type")
	}
	// Lock table output should reference aws_dynamodb_table.<name>.id
	if !strings.Contains(output, "aws_dynamodb_table.") {
		t.Error("Terraform output should reference aws_dynamodb_table resource type")
	}
	// Output values must NOT use HCL1 ${} interpolation — HCL2 uses bare references
	if strings.Contains(output, "${") {
		t.Error("Terraform output must not contain ${} interpolation syntax (invalid HCL2)")
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

// TestDeviceNameForModule verifies that deviceNameForModule returns the correct
// EBS device name per module. Workstation uses /dev/sda1 (root EBS), all others
// use /dev/sdf. Regression test for: workstation was hardcoded to /dev/sdf.
func TestDeviceNameForModule(t *testing.T) {
	// Workstation uses /dev/sda1 (root EBS)
	if deviceNameForModule("workstation") != "/dev/sda1" {
		t.Errorf("workstation device name = %q, want %q", deviceNameForModule("workstation"), "/dev/sda1")
	}
	// All other modules use /dev/sdf
	for _, mod := range []string{"horde", "perforce", "lore", "ddc", "ci", "deploy", ""} {
		if deviceNameForModule(mod) != "/dev/sdf" {
			t.Errorf("deviceNameForModule(%q) = %q, want %q", mod, deviceNameForModule(mod), "/dev/sdf")
		}
	}
}

// TestWorkstationBlockDeviceMapping verifies that a workstation EC2 instance
// gets /dev/sda1 in its BlockDeviceMappings (not /dev/sdf).
func TestWorkstationBlockDeviceMapping(t *testing.T) {
	res := state.ModuleResource{
		TypeName:   "AWS::EC2::Instance",
		Identifier: "i-ws123",
		Properties: map[string]string{
			"instanceType": "g4dn.xlarge",
			"volumeSize":   "100",
		},
	}
	cfg := config.Defaults()
	cfg.Workstation.AmiID = "ami-ws123"
	cfg.Workstation.InstanceType = "g4dn.xlarge"

	props := extractProperties("workstation", res, cfg)
	bdm, ok := props["BlockDeviceMappings"].([]map[string]any)
	if !ok || len(bdm) == 0 {
		t.Fatal("expected BlockDeviceMappings for workstation instance")
	}
	if bdm[0]["DeviceName"] != "/dev/sda1" {
		t.Errorf("workstation device name = %q, want %q", bdm[0]["DeviceName"], "/dev/sda1")
	}
}

// TestHordeBlockDeviceMapping verifies that a horde EC2 instance
// gets /dev/sdf in its BlockDeviceMappings (the non-workstation default).
func TestHordeBlockDeviceMapping(t *testing.T) {
	res := state.ModuleResource{
		TypeName:   "AWS::EC2::Instance",
		Identifier: "i-horde123",
		Properties: map[string]string{
			"instanceType": "m7i.2xlarge",
			"volumeSize":   "100",
		},
	}
	cfg := testConfigWithHorde()

	props := extractProperties("horde", res, cfg)
	bdm, ok := props["BlockDeviceMappings"].([]map[string]any)
	if !ok || len(bdm) == 0 {
		t.Fatal("expected BlockDeviceMappings for horde instance")
	}
	if bdm[0]["DeviceName"] != "/dev/sdf" {
		t.Errorf("horde device name = %q, want %q", bdm[0]["DeviceName"], "/dev/sdf")
	}
}

// TestCodeBuildArtifactsNoArtifacts verifies that the CI CodeBuild project
// defaults to NO_ARTIFACTS (matching production internal/cloud/aws/codebuild.go).
// Regression test for: export was defaulting to S3 instead of NO_ARTIFACTS.
func TestCodeBuildArtifactsNoArtifacts(t *testing.T) {
	res := state.ModuleResource{
		TypeName:   "AWS::CodeBuild::Project",
		Identifier: "fabrica-ci",
		Properties: map[string]string{},
	}
	cfg := config.Defaults()

	props := extractProperties("ci", res, cfg)
	artifacts, ok := props["Artifacts"].(map[string]any)
	if !ok {
		t.Fatal("expected Artifacts in CI extractProperties")
	}
	if artifacts["Type"] != "NO_ARTIFACTS" {
		t.Errorf("CodeBuild Artifacts Type = %q, want %q", artifacts["Type"], "NO_ARTIFACTS")
	}
}

// TestCodeBuildArtifactsStatePreserved verifies that when state already has
// an Artifacts property, extractProperties does not override it.
func TestCodeBuildArtifactsStatePreserved(t *testing.T) {
	res := state.ModuleResource{
		TypeName:   "AWS::CodeBuild::Project",
		Identifier: "fabrica-ci",
		Properties: map[string]string{
			"Artifacts": `{"Type":"S3","Location":"my-bucket"}`,
		},
	}
	cfg := config.Defaults()

	props := extractProperties("ci", res, cfg)
	// The state-provided Artifacts should be preserved (it's a string in state,
	// not a map, so the !ok check won't trigger the default)
	if _, ok := props["Artifacts"]; !ok {
		t.Error("expected Artifacts to be present from state")
	}
}

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
	// V2: state-backend + workstation + ci + deploy = 4 modules
	if len(modules) != 4 {
		t.Errorf("expected 4 modules (state-backend + workstation + ci + deploy), got %d", len(modules))
	}
	// Verify module names
	expectedNames := map[string]bool{"state-backend": false, "workstation": false, "ci": false, "deploy": false}
	for _, m := range modules {
		if _, ok := expectedNames[m.Name]; ok {
			expectedNames[m.Name] = true
		}
	}
	for name, found := range expectedNames {
		if !found {
			t.Errorf("expected module %s not found", name)
		}
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

// TestTerraformResourceName covers CamelCase→snake_case conversion for logical IDs.
func TestTerraformResourceName(t *testing.T) {
	gen := &terraformGenerator{}

	tests := []struct {
		input    string
		expected string
	}{
		{"HordeSecurityGroupI", "horde_security_group_i"},
		{"HordeInstanceI", "horde_instance_i"},
		{"my_resource", "my_resource"},
		{"I", "i"},
		{"", ""},
		{"HordeInstance1", "horde_instance1"},
		{"SGI", "s_g_i"},
	}

	for _, tc := range tests {
		got := gen.tfResourceName(tc.input)
		if got != tc.expected {
			t.Errorf("tfResourceName(%q) = %q, want %q", tc.input, got, tc.expected)
		}
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

// TestSgRulesForModuleWorkstationDefaultCIDR verifies that the workstation
// module's default CIDR matches its actual create default (10.0.0.0/8, not
// 0.0.0.0/0), so the export reflects what create would actually provision.
func TestSgRulesForModuleWorkstationDefaultCIDR(t *testing.T) {
	cfg := config.Defaults()
	rules := sgRulesForModule("workstation", cfg)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule for workstation, got %d", len(rules))
	}
	cidr, ok := rules[0]["CidrIp"].(string)
	if !ok || cidr != "10.0.0.0/8" {
		t.Errorf("workstation default CIDR = %q, want 10.0.0.0/8", cidr)
	}
}

// TestModuleDefaultCIDR verifies moduleDefaultCIDR returns the correct default
// for each module.
func TestModuleDefaultCIDR(t *testing.T) {
	for _, mod := range []string{"horde", "perforce", "lore", "ddc", "workstation"} {
		cidr := moduleDefaultCIDR(mod)
		if cidr != "10.0.0.0/8" {
			t.Errorf("%s default CIDR = %q, want 10.0.0.0/8", mod, cidr)
		}
	}
	// Unknown module also falls back to 10.0.0.0/8.
	if moduleDefaultCIDR("unknown") != "10.0.0.0/8" {
		t.Error("unknown module should default to 10.0.0.0/8")
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
	pab := map[string]any{
		"BlockPublicAcls":       true,
		"BlockPublicPolicy":     true,
		"IgnorePublicAcls":      true,
		"RestrictPublicBuckets": true,
	}
	out := gen.hclPublicAccess(pab)
	if !strings.Contains(out, "block_public_acls = true") {
		t.Error("hclPublicAccess should output block_public_acls")
	}
	if !strings.Contains(out, "block_public_policy = true") {
		t.Error("hclPublicAccess should output block_public_policy")
	}
	if !strings.Contains(out, "ignore_public_acls = true") {
		t.Error("hclPublicAccess should output ignore_public_acls")
	}
	if !strings.Contains(out, "restrict_public_buckets = true") {
		t.Error("hclPublicAccess should output restrict_public_buckets")
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
	doc := map[string]any{
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
	out := gen.hclPolicyDoc(doc)
	if !strings.Contains(out, "2012-10-17") {
		t.Error("hclPolicyDoc should output version")
	}
	if !strings.Contains(out, "Allow") {
		t.Error("hclPolicyDoc should output effect from statement")
	}
	if !strings.Contains(out, "ec2.amazonaws.com") {
		t.Error("hclPolicyDoc should output service from principal")
	}
	if !strings.Contains(out, "sts:AssumeRole") {
		t.Error("hclPolicyDoc should output action from statement")
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

// TestToLogicalIDEmpty covers toLogicalID with empty and stripped-to-empty identifiers.
func TestToLogicalIDEmpty(t *testing.T) {
	tests := []struct {
		name       string
		identifier string
		wantPrefix string
	}{
		{
			name:       "empty string",
			identifier: "",
			wantPrefix: "HORDEInstanceX",
		},
		{
			name:       "all dashes strips to empty",
			identifier: "---",
			wantPrefix: "HORDEInstanceX",
		},
		{
			name:       "all underscores strips to empty",
			identifier: "___",
			wantPrefix: "HORDEInstanceX",
		},
		{
			name:       "all dots strips to empty",
			identifier: "...",
			wantPrefix: "HORDEInstanceX",
		},
		{
			name:       "normal identifier",
			identifier: "i-12345",
			wantPrefix: "HORDEInstanceI",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toLogicalID("horde", "AWS::EC2::Instance", tt.identifier)
			if !strings.HasPrefix(got, tt.wantPrefix) {
				t.Errorf("toLogicalID(%q) = %q, want prefix %q", tt.identifier, got, tt.wantPrefix)
			}
		})
	}
}

// TestToLogicalIDNoCollision verifies that multiple resources of the same type
// within one module get distinct logical IDs (DDC home+edge, Deploy fleets).
func TestToLogicalIDNoCollision(t *testing.T) {
	// DDC home and edge security groups should have distinct IDs
	homeSG := toLogicalID("ddc", "AWS::EC2::SecurityGroup", "sg-ddc123")
	edgeSG := toLogicalID("ddc", "AWS::EC2::SecurityGroup", "sg-edge456")
	if homeSG == edgeSG {
		t.Errorf("DDC home and edge SGs should have distinct IDs: both %s", homeSG)
	}

	// DDC home and edge instances should have distinct IDs
	homeInst := toLogicalID("ddc", "AWS::EC2::Instance", "i-home123")
	edgeInst := toLogicalID("ddc", "AWS::EC2::Instance", "i-edge456")
	if homeInst == edgeInst {
		t.Errorf("DDC home and edge instances should have distinct IDs: both %s", homeInst)
	}

	// Deploy fleets should have distinct IDs
	fleet1 := toLogicalID("deploy", "AWS::GameLift::Fleet", "fleet-abc123")
	fleet2 := toLogicalID("deploy", "AWS::GameLift::Fleet", "fleet-def456")
	if fleet1 == fleet2 {
		t.Errorf("Deploy fleets should have distinct IDs: both %s", fleet1)
	}

	// Long identifiers should be truncated to 12 chars
	longID := toLogicalID("deploy", "AWS::GameLift::Fleet", "fleet-verylongidentifier12345")
	if len(longID) > len("DEPLOYFleet")+12 {
		t.Errorf("long ID should be truncated: %s (len %d)", longID, len(longID))
	}
}

// TestHclPolicyDocNonEC2 covers hclPolicyDoc with a non-EC2 policy document.
func TestHclPolicyDocNonEC2(t *testing.T) {
	gen := &terraformGenerator{}
	doc := map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Effect": "Allow",
				"Principal": map[string]any{
					"Service": "codebuild.amazonaws.com",
				},
				"Action": "sts:AssumeRole",
			},
		},
	}
	out := gen.hclPolicyDoc(doc)
	if !strings.Contains(out, "codebuild.amazonaws.com") {
		t.Error("hclPolicyDoc should read service from actual policy, not hardcode ec2")
	}
	if !strings.Contains(out, "2012-10-17") {
		t.Error("hclPolicyDoc should output version from document")
	}
}

// TestHclPolicyDocEmptyStatement covers hclPolicyDoc with no statements.
func TestHclPolicyDocEmptyStatement(t *testing.T) {
	gen := &terraformGenerator{}
	doc := map[string]any{
		"Version":   "2012-10-17",
		"Statement": []map[string]any{},
	}
	out := gen.hclPolicyDoc(doc)
	if !strings.Contains(out, "2012-10-17") {
		t.Error("hclPolicyDoc should output version")
	}
	if !strings.Contains(out, "Statement = []") {
		t.Error("hclPolicyDoc should output empty statement list")
	}
}

// TestHclPublicAccessEmptyMap covers hclPublicAccess with an empty map.
func TestHclPublicAccessEmptyMap(t *testing.T) {
	gen := &terraformGenerator{}
	out := gen.hclPublicAccess(map[string]any{})
	if !strings.Contains(out, "S3 public access block") {
		t.Error("hclPublicAccess should output comment for empty map")
	}
}

// TestHclStringList covers hclStringList formatting.
func TestHclStringList(t *testing.T) {
	gen := &terraformGenerator{}
	out := gen.hclPolicyDoc(map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Effect": "Allow",
				"Principal": map[string]any{
					"Service": []string{"ec2.amazonaws.com", "lambda.amazonaws.com"},
				},
				"Action": []string{"sts:AssumeRole", "sts:DecodeAuthorizationMessage"},
			},
		},
	})
	if !strings.Contains(out, "ec2.amazonaws.com") {
		t.Error("hclStringList should include first service")
	}
	if !strings.Contains(out, "lambda.amazonaws.com") {
		t.Error("hclStringList should include second service")
	}
	if !strings.Contains(out, "sts:DecodeAuthorizationMessage") {
		t.Error("hclStringList should include second action")
	}
}

// ---- V2 module builder tests ----

func testConfigWithDDC() *config.Config {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.DDC.AmiID = "ami-ddc123"
	cfg.DDC.InstanceType = "m5.xlarge"
	cfg.DDC.AllowedCIDR = "10.0.0.0/8"
	return cfg
}

func testConfigWithWorkstation() *config.Config {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.Workstation.AmiID = "ami-ws123"
	cfg.Workstation.InstanceType = "g4dn.xlarge"
	cfg.Workstation.AllowedCIDR = "10.0.0.0/8"
	return cfg
}

func testConfigWithCI() *config.Config {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.CI.ProjectName = "fabrica-ci"
	cfg.CI.ComputeType = "BUILD_GENERAL1_SMALL"
	cfg.CI.Image = "aws/codebuild/standard:7.0"
	return cfg
}

func testConfigWithDeploy() *config.Config {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.Deploy.AliasName = "fabrica-deploy"
	return cfg
}

func TestBuildDDCModule(t *testing.T) {
	ms := state.ModuleState{
		Name:   "ddc",
		Status: "ready",
		Resources: []state.ModuleResource{
			{
				TypeName:   "AWS::EC2::SecurityGroup",
				Identifier: "sg-ddc123",
				Properties: map[string]string{
					"GroupName": "fabrica-ddc-sg",
					"VpcId":     "vpc-0abc123",
				},
			},
			{
				TypeName:   "AWS::EC2::Instance",
				Identifier: "i-ddc-coord",
				Properties: map[string]string{
					"instanceType": "m5.xlarge",
					"volumeSize":   "500",
					"role":         "coordinator",
				},
			},
			{
				TypeName:   "AWS::S3::Bucket",
				Identifier: "fabrica-ddc-bucket-123",
				Properties: map[string]string{
					"BucketName": "fabrica-ddc-bucket-123",
				},
			},
			{
				TypeName:   "AWS::IAM::Role",
				Identifier: "fabrica-ddc-role",
				Properties: map[string]string{
					"RoleName": "fabrica-ddc-role",
				},
			},
			{
				TypeName:   "AWS::IAM::InstanceProfile",
				Identifier: "fabrica-ddc-profile",
				Properties: map[string]string{
					"InstanceProfileName": "fabrica-ddc-profile",
				},
			},
		},
	}
	cfg := testConfigWithDDC()
	mod := buildDDCModule(ms, cfg)
	if mod.Name != "ddc" {
		t.Errorf("unexpected module name: %s", mod.Name)
	}
	if mod.Status != "ready" {
		t.Errorf("unexpected status: %s", mod.Status)
	}
	if len(mod.Resources) != 5 {
		t.Errorf("expected 5 resources, got %d", len(mod.Resources))
	}

	// Verify S3 bucket is included
	foundBucket := false
	for _, r := range mod.Resources {
		if r.TypeName == "AWS::S3::Bucket" {
			foundBucket = true
			if r.Properties["BucketName"] != "fabrica-ddc-bucket-123" {
				t.Errorf("unexpected bucket name: %v", r.Properties["BucketName"])
			}
		}
	}
	if !foundBucket {
		t.Error("expected S3 bucket resource")
	}
}

func TestBuildDDCModuleEmpty(t *testing.T) {
	ms := state.ModuleState{
		Name:      "ddc",
		Status:    "ready",
		Resources: []state.ModuleResource{},
	}
	cfg := testConfigWithDDC()
	mod := buildDDCModule(ms, cfg)
	if mod.Name != "ddc" {
		t.Errorf("unexpected module name: %s", mod.Name)
	}
	if len(mod.Resources) != 0 {
		t.Errorf("expected 0 resources, got %d", len(mod.Resources))
	}
}

func TestBuildWorkstationModule(t *testing.T) {
	ms := state.ModuleState{
		Name:   "workstation",
		Status: "ready",
		Resources: []state.ModuleResource{
			{
				TypeName:   "AWS::EC2::SecurityGroup",
				Identifier: "sg-ws123",
				Properties: map[string]string{
					"GroupName": "fabrica-workstation-sg",
					"VpcId":     "vpc-0abc123",
				},
			},
			{
				TypeName:   "AWS::EC2::Instance",
				Identifier: "i-ws123",
				Properties: map[string]string{
					"instanceType": "g4dn.xlarge",
					"volumeSize":   "100",
				},
			},
		},
	}
	cfg := testConfigWithWorkstation()
	mod := buildWorkstationModule(ms, cfg)
	if mod.Name != "workstation" {
		t.Errorf("unexpected module name: %s", mod.Name)
	}
	if len(mod.Resources) != 2 {
		t.Errorf("expected 2 resources, got %d", len(mod.Resources))
	}
}

func TestBuildWorkstationModuleEmpty(t *testing.T) {
	ms := state.ModuleState{
		Name:      "workstation",
		Status:    "ready",
		Resources: []state.ModuleResource{},
	}
	mod := buildWorkstationModule(ms, testConfigWithWorkstation())
	if len(mod.Resources) != 0 {
		t.Errorf("expected 0 resources, got %d", len(mod.Resources))
	}
}

func TestBuildCIModule(t *testing.T) {
	ms := state.ModuleState{
		Name:   "ci",
		Status: "ready",
		Resources: []state.ModuleResource{
			{
				TypeName:   "AWS::IAM::Role",
				Identifier: "fabrica-ci-codebuild",
				Properties: map[string]string{
					"RoleName": "fabrica-ci-codebuild",
				},
			},
			{
				TypeName:   "AWS::CodeBuild::Project",
				Identifier: "fabrica-ci",
				Properties: map[string]string{
					"Name": "fabrica-ci",
				},
			},
		},
	}
	cfg := testConfigWithCI()
	mod := buildCIModule(ms, cfg)
	if mod.Name != "ci" {
		t.Errorf("unexpected module name: %s", mod.Name)
	}
	if len(mod.Resources) != 2 {
		t.Errorf("expected 2 resources, got %d", len(mod.Resources))
	}

	// Verify CodeBuild project
	foundProject := false
	for _, r := range mod.Resources {
		if r.TypeName == "AWS::CodeBuild::Project" {
			foundProject = true
			if r.Properties["Name"] != "fabrica-ci" {
				t.Errorf("unexpected project name: %v", r.Properties["Name"])
			}
		}
	}
	if !foundProject {
		t.Error("expected CodeBuild project resource")
	}
}

func TestBuildCIModuleEmpty(t *testing.T) {
	ms := state.ModuleState{
		Name:      "ci",
		Status:    "ready",
		Resources: []state.ModuleResource{},
	}
	mod := buildCIModule(ms, testConfigWithCI())
	if len(mod.Resources) != 0 {
		t.Errorf("expected 0 resources, got %d", len(mod.Resources))
	}
}

func TestBuildDeployModule(t *testing.T) {
	ms := state.ModuleState{
		Name:   "deploy",
		Status: "ready",
		Resources: []state.ModuleResource{
			{
				TypeName:   "AWS::IAM::Role",
				Identifier: "fabrica-deploy-gamelift",
				Properties: map[string]string{
					"RoleName": "fabrica-deploy-gamelift",
				},
			},
			{
				TypeName:   "AWS::GameLift::Alias",
				Identifier: "alias-1",
				Properties: map[string]string{
					"Name": "fabrica-deploy",
				},
			},
			{
				TypeName:   "AWS::GameLift::Fleet",
				Identifier: "fleet-1",
				Properties: map[string]string{
					"Name": "fabrica-deploy-fleet",
					"role": "active",
				},
			},
			{
				TypeName:   "AWS::GameLift::Build",
				Identifier: "build-1",
				Properties: map[string]string{
					"Name": "fabrica-deploy-build",
				},
			},
		},
	}
	cfg := testConfigWithDeploy()
	mod := buildDeployModule(ms, cfg)
	if mod.Name != "deploy" {
		t.Errorf("unexpected module name: %s", mod.Name)
	}
	if len(mod.Resources) != 4 {
		t.Errorf("expected 4 resources, got %d", len(mod.Resources))
	}

	// Verify GameLift alias
	foundAlias := false
	for _, r := range mod.Resources {
		if r.TypeName == "AWS::GameLift::Alias" {
			foundAlias = true
		}
	}
	if !foundAlias {
		t.Error("expected GameLift alias resource")
	}
}

func TestBuildDeployModuleEmpty(t *testing.T) {
	ms := state.ModuleState{
		Name:      "deploy",
		Status:    "ready",
		Resources: []state.ModuleResource{},
	}
	mod := buildDeployModule(ms, testConfigWithDeploy())
	if len(mod.Resources) != 0 {
		t.Errorf("expected 0 resources, got %d", len(mod.Resources))
	}
}

// TestExtractPropertiesCodeBuild verifies CodeBuild project property extraction.
func TestExtractPropertiesCodeBuild(t *testing.T) {
	res := state.ModuleResource{
		TypeName:   "AWS::CodeBuild::Project",
		Identifier: "fabrica-ci",
		Properties: map[string]string{
			"Name": "fabrica-ci",
		},
	}
	cfg := testConfigWithCI()
	props := extractProperties("ci", res, cfg)

	if props["Name"] != "fabrica-ci" {
		t.Errorf("unexpected project name: %v", props["Name"])
	}
	// ServiceRole is intentionally omitted — the ARN is account-specific
	// and not reconstructible from local state.
	if props["ServiceRole"] != nil {
		t.Error("ServiceRole should be omitted (account-specific ARN)")
	}
	if props["Environment"] == nil {
		t.Error("expected Environment to be set")
	}
	if props["Artifacts"] == nil {
		t.Error("expected Artifacts to be set")
	}
}

// TestExtractPropertiesGameLiftAlias verifies GameLift alias property extraction.
func TestExtractPropertiesGameLiftAlias(t *testing.T) {
	res := state.ModuleResource{
		TypeName:   "AWS::GameLift::Alias",
		Identifier: "alias-1",
		Properties: map[string]string{},
	}
	cfg := testConfigWithDeploy()
	props := extractProperties("deploy", res, cfg)

	if props["Name"] != "fabrica-deploy" {
		t.Errorf("unexpected alias name: %v", props["Name"])
	}
}

// TestExtractPropertiesGameLiftFleet verifies GameLift fleet property extraction.
func TestExtractPropertiesGameLiftFleet(t *testing.T) {
	res := state.ModuleResource{
		TypeName:   "AWS::GameLift::Fleet",
		Identifier: "fleet-1",
		Properties: map[string]string{
			"role": "active",
		},
	}
	props := extractProperties("deploy", res, config.Defaults())

	if props["Name"] != "fabrica-deploy-fleet" {
		t.Errorf("unexpected fleet name: %v", props["Name"])
	}
	if props["Tags"] == nil {
		t.Error("expected Tags to be set")
	}
}

// TestExtractPropertiesGameLiftBuild verifies GameLift build property extraction.
func TestExtractPropertiesGameLiftBuild(t *testing.T) {
	res := state.ModuleResource{
		TypeName:   "AWS::GameLift::Build",
		Identifier: "build-1",
		Properties: map[string]string{},
	}
	props := extractProperties("deploy", res, config.Defaults())

	if props["Name"] != "fabrica-deploy-build" {
		t.Errorf("unexpected build name: %v", props["Name"])
	}
}

// TestExtractPropertiesS3Bucket verifies S3 bucket property extraction (DDC).
func TestExtractPropertiesS3Bucket(t *testing.T) {
	res := state.ModuleResource{
		TypeName:   "AWS::S3::Bucket",
		Identifier: "fabrica-ddc-bucket-123",
		Properties: map[string]string{
			"BucketName": "fabrica-ddc-bucket-123",
		},
	}
	props := extractProperties("ddc", res, config.Defaults())

	if props["BucketName"] != "fabrica-ddc-bucket-123" {
		t.Errorf("unexpected bucket name: %v", props["BucketName"])
	}
	if props["Tags"] == nil {
		t.Error("expected Tags to be set")
	}
}

// TestExtractPropertiesS3BucketDefault verifies default bucket name when not in state.
func TestExtractPropertiesS3BucketDefault(t *testing.T) {
	res := state.ModuleResource{
		TypeName:   "AWS::S3::Bucket",
		Identifier: "some-bucket",
		Properties: map[string]string{},
	}
	props := extractProperties("ddc", res, config.Defaults())

	if props["BucketName"] != "fabrica-ddc-bucket" {
		t.Errorf("unexpected default bucket name: %v", props["BucketName"])
	}
}

// TestDDCRedaction verifies UserData is redacted for DDC instances.
func TestDDCRedaction(t *testing.T) {
	ms := state.ModuleState{
		Name:   "ddc",
		Status: "ready",
		Resources: []state.ModuleResource{
			{
				TypeName:   "AWS::EC2::Instance",
				Identifier: "i-ddc",
				Properties: map[string]string{
					"instanceType": "m5.xlarge",
					"UserData":     strings.Repeat("A", 300),
				},
			},
		},
	}
	mod := buildDDCModule(ms, testConfigWithDDC())
	for _, r := range mod.Resources {
		if r.TypeName == "AWS::EC2::Instance" {
			if u, ok := r.Properties["UserData"].(string); ok && strings.HasPrefix(u, "# REDACTED") {
				return
			}
			t.Error("UserData should be redacted in DDC export")
		}
	}
}

// TestWorkstationRedaction verifies UserData is redacted for Workstation instances.
func TestWorkstationRedaction(t *testing.T) {
	ms := state.ModuleState{
		Name:   "workstation",
		Status: "ready",
		Resources: []state.ModuleResource{
			{
				TypeName:   "AWS::EC2::Instance",
				Identifier: "i-ws",
				Properties: map[string]string{
					"instanceType": "g4dn.xlarge",
					"UserData":     strings.Repeat("B", 300),
				},
			},
		},
	}
	mod := buildWorkstationModule(ms, testConfigWithWorkstation())
	for _, r := range mod.Resources {
		if r.TypeName == "AWS::EC2::Instance" {
			if u, ok := r.Properties["UserData"].(string); ok && strings.HasPrefix(u, "# REDACTED") {
				return
			}
			t.Error("UserData should be redacted in Workstation export")
		}
	}
}

// TestMixedV1V2CloudFormation verifies a mixed V1+V2 state produces valid CFN output.
func TestMixedV1V2CloudFormation(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	// V1 module
	st.UpsertModule("horde", "ami-0abc123def456", "ready", []state.ModuleResource{
		{
			TypeName:   "AWS::EC2::SecurityGroup",
			Identifier: "sg-0a1b2c3d4e5f67890",
			Properties: map[string]string{"GroupName": "fabrica-horde-sg", "VpcId": "vpc-0abc123"},
		},
		{
			TypeName:   "AWS::EC2::Instance",
			Identifier: "i-0abc123def456",
			Properties: map[string]string{"instanceType": "m7i.2xlarge", "volumeSize": "100"},
		},
	})
	// V2 module — DDC
	st.UpsertModule("ddc", "ami-ddc", "ready", []state.ModuleResource{
		{
			TypeName:   "AWS::EC2::Instance",
			Identifier: "i-ddc-coord",
			Properties: map[string]string{"instanceType": "m5.xlarge", "volumeSize": "500", "role": "coordinator"},
		},
		{
			TypeName:   "AWS::S3::Bucket",
			Identifier: "fabrica-ddc-bucket-123",
			Properties: map[string]string{"BucketName": "fabrica-ddc-bucket-123"},
		},
	})
	// V2 module — CI
	st.UpsertModule("ci", "fabrica-ci", "ready", []state.ModuleResource{
		{
			TypeName:   "AWS::IAM::Role",
			Identifier: "fabrica-ci-codebuild",
			Properties: map[string]string{"RoleName": "fabrica-ci-codebuild"},
		},
		{
			TypeName:   "AWS::CodeBuild::Project",
			Identifier: "fabrica-ci",
			Properties: map[string]string{"Name": "fabrica-ci"},
		},
	})

	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.Horde.AmiID = "ami-0abc123def456"
	cfg.Horde.InstanceType = "m7i.2xlarge"
	cfg.DDC.AmiID = "ami-ddc"
	cfg.DDC.InstanceType = "m5.xlarge"
	cfg.CI.ProjectName = "fabrica-ci"

	data, err := GenerateOutput(CloudFormation, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var tmpl map[string]any
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		t.Fatalf("invalid YAML output: %v\n%s", err, string(data))
	}

	resources := tmpl["Resources"].(map[string]any)

	// Should have state backend + horde + ddc + ci resources
	if len(resources) < 8 {
		t.Errorf("expected at least 8 resources, got %d", len(resources))
	}

	// Check V1 resources
	if _, ok := resources["FabricaStateBucket"]; !ok {
		t.Error("missing FabricaStateBucket resource")
	}
	if _, ok := resources["FabricaStateLockTable"]; !ok {
		t.Error("missing FabricaStateLockTable resource")
	}

	// Check V2 DDC resources
	foundDDC := false
	for _, res := range resources {
		rm := res.(map[string]any)
		if typ, ok := rm["Type"].(string); ok {
			if typ == "AWS::EC2::Instance" || typ == "AWS::S3::Bucket" {
				// DDC has instances and buckets
				if props, ok := rm["Properties"].(map[string]any); ok {
					if bn, ok := props["BucketName"].(string); ok && strings.Contains(bn, "ddc") {
						foundDDC = true
					}
				}
			}
		}
	}
	if !foundDDC {
		t.Error("expected DDC resources in output")
	}

	// Check V2 CI resources
	foundCodeBuild := false
	for _, res := range resources {
		rm := res.(map[string]any)
		if typ, ok := rm["Type"].(string); ok && typ == "AWS::CodeBuild::Project" {
			foundCodeBuild = true
		}
	}
	if !foundCodeBuild {
		t.Error("expected CodeBuild project in output")
	}

	// Verify metadata version is v2
	metadata := tmpl["Metadata"].(map[string]any)
	fabricaExport := metadata["FabricaExport"].(map[string]any)
	if fabricaExport["Version"] != "v2" {
		t.Errorf("expected version v2, got %v", fabricaExport["Version"])
	}
}

// TestMixedV1V2Terraform verifies a mixed V1+V2 state produces valid TF output.
func TestMixedV1V2Terraform(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	// V1 module
	st.UpsertModule("perforce", "v23.2", "ready", []state.ModuleResource{
		{
			TypeName:   "AWS::EC2::SecurityGroup",
			Identifier: "sg-p4",
			Properties: map[string]string{"GroupName": "fabrica-perforce-sg", "VpcId": "vpc-0abc123"},
		},
		{
			TypeName:   "AWS::EC2::Instance",
			Identifier: "i-p4",
			Properties: map[string]string{"instanceType": "c5.2xlarge", "volumeSize": "500"},
		},
	})
	// V2 module — Deploy
	st.UpsertModule("deploy", "v1.0.0", "ready", []state.ModuleResource{
		{
			TypeName:   "AWS::GameLift::Alias",
			Identifier: "alias-1",
			Properties: map[string]string{"Name": "fabrica-deploy"},
		},
		{
			TypeName:   "AWS::GameLift::Fleet",
			Identifier: "fleet-1",
			Properties: map[string]string{"Name": "fabrica-deploy-fleet", "role": "active"},
		},
	})

	cfg := config.Defaults()
	cfg.Perforce.InstanceType = "c5.2xlarge"
	cfg.Deploy.AliasName = "fabrica-deploy"

	data, err := GenerateOutput(Terraform, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := string(data)

	// V1 resources
	if !strings.Contains(output, `resource "aws_instance"`) {
		t.Error("missing aws_instance resource")
	}
	if !strings.Contains(output, `resource "aws_s3_bucket"`) {
		t.Error("missing aws_s3_bucket resource")
	}

	// V2 resources
	if !strings.Contains(output, `resource "aws_gamelift_alias"`) {
		t.Error("missing aws_gamelift_alias resource")
	}
	if !strings.Contains(output, `resource "aws_gamelift_fleet"`) {
		t.Error("missing aws_gamelift_fleet resource")
	}

	// No ${} interpolation
	if strings.Contains(output, "${") {
		t.Error("Terraform output must not contain ${} interpolation")
	}

	// V2 header
	if !strings.Contains(output, "(V2)") {
		t.Error("expected V2 in header comment")
	}
}

// TestDDCTerraformOutput verifies DDC resources in Terraform output.
func TestDDCTerraformOutput(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("ddc", "ami-ddc", "ready", []state.ModuleResource{
		{
			TypeName:   "AWS::EC2::Instance",
			Identifier: "i-ddc-coord",
			Properties: map[string]string{"instanceType": "m5.xlarge", "volumeSize": "500"},
		},
		{
			TypeName:   "AWS::S3::Bucket",
			Identifier: "fabrica-ddc-bucket-123",
			Properties: map[string]string{"BucketName": "fabrica-ddc-bucket-123"},
		},
		{
			TypeName:   "AWS::IAM::Role",
			Identifier: "fabrica-ddc-role",
			Properties: map[string]string{"RoleName": "fabrica-ddc-role"},
		},
	})

	cfg := testConfigWithDDC()
	data, err := GenerateOutput(Terraform, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := string(data)

	if !strings.Contains(output, `resource "aws_s3_bucket"`) {
		t.Error("missing aws_s3_bucket resource for DDC")
	}
	if !strings.Contains(output, `resource "aws_instance"`) {
		t.Error("missing aws_instance resource for DDC")
	}
	if !strings.Contains(output, `resource "aws_iam_role"`) {
		t.Error("missing aws_iam_role resource for DDC")
	}
}

// TestCITerraformOutput verifies CI resources in Terraform output.
func TestCITerraformOutput(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "fabrica-ci", "ready", []state.ModuleResource{
		{
			TypeName:   "AWS::IAM::Role",
			Identifier: "fabrica-ci-codebuild",
			Properties: map[string]string{"RoleName": "fabrica-ci-codebuild"},
		},
		{
			TypeName:   "AWS::CodeBuild::Project",
			Identifier: "fabrica-ci",
			Properties: map[string]string{"Name": "fabrica-ci"},
		},
	})

	cfg := testConfigWithCI()
	data, err := GenerateOutput(Terraform, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := string(data)

	if !strings.Contains(output, `resource "aws_codebuild_project"`) {
		t.Error("missing aws_codebuild_project resource")
	}
	if !strings.Contains(output, `resource "aws_iam_role"`) {
		t.Error("missing aws_iam_role resource")
	}
	// Verify CodeBuild environment block
	if !strings.Contains(output, "environment {") {
		t.Error("missing CodeBuild environment block")
	}
	if !strings.Contains(output, "compute_type") {
		t.Error("missing compute_type in CodeBuild environment")
	}
}

// TestDeployTerraformOutput verifies Deploy resources in Terraform output.
func TestDeployTerraformOutput(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("deploy", "v1.0.0", "ready", []state.ModuleResource{
		{
			TypeName:   "AWS::GameLift::Alias",
			Identifier: "alias-1",
			Properties: map[string]string{"Name": "fabrica-deploy"},
		},
		{
			TypeName:   "AWS::GameLift::Fleet",
			Identifier: "fleet-1",
			Properties: map[string]string{"Name": "fabrica-deploy-fleet"},
		},
		{
			TypeName:   "AWS::GameLift::Build",
			Identifier: "build-1",
			Properties: map[string]string{"Name": "fabrica-deploy-build"},
		},
	})

	cfg := testConfigWithDeploy()
	data, err := GenerateOutput(Terraform, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := string(data)

	if !strings.Contains(output, `resource "aws_gamelift_alias"`) {
		t.Error("missing aws_gamelift_alias resource")
	}
	if !strings.Contains(output, `resource "aws_gamelift_fleet"`) {
		t.Error("missing aws_gamelift_fleet resource")
	}
	if !strings.Contains(output, `resource "aws_gamelift_build"`) {
		t.Error("missing aws_gamelift_build resource")
	}
}

// TestWorkstationTerraformOutput verifies Workstation resources in Terraform output.
func TestWorkstationTerraformOutput(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("workstation", "ami-ws", "ready", []state.ModuleResource{
		{
			TypeName:   "AWS::EC2::SecurityGroup",
			Identifier: "sg-ws123",
			Properties: map[string]string{"GroupName": "fabrica-workstation-sg", "VpcId": "vpc-0abc123"},
		},
		{
			TypeName:   "AWS::EC2::Instance",
			Identifier: "i-ws123",
			Properties: map[string]string{"instanceType": "g4dn.xlarge", "volumeSize": "100"},
		},
	})

	cfg := testConfigWithWorkstation()
	data, err := GenerateOutput(Terraform, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := string(data)

	if !strings.Contains(output, `resource "aws_instance"`) {
		t.Error("missing aws_instance resource for Workstation")
	}
	if !strings.Contains(output, `resource "aws_security_group"`) {
		t.Error("missing aws_security_group resource for Workstation")
	}
}

// TestDDCCloudFormationOutput verifies DDC resources in CFN output.
func TestDDCCloudFormationOutput(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("ddc", "ami-ddc", "ready", []state.ModuleResource{
		{
			TypeName:   "AWS::EC2::Instance",
			Identifier: "i-ddc-coord",
			Properties: map[string]string{"instanceType": "m5.xlarge", "volumeSize": "500"},
		},
		{
			TypeName:   "AWS::S3::Bucket",
			Identifier: "fabrica-ddc-bucket-123",
			Properties: map[string]string{"BucketName": "fabrica-ddc-bucket-123"},
		},
	})

	cfg := testConfigWithDDC()
	data, err := GenerateOutput(CloudFormation, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var tmpl map[string]any
	if err := yaml.Unmarshal(data, &tmpl); err != nil {
		t.Fatalf("invalid YAML: %v\n%s", err, string(data))
	}

	resources := tmpl["Resources"].(map[string]any)

	// Should have state backend + DDC resources
	if len(resources) < 4 {
		t.Errorf("expected at least 4 resources, got %d", len(resources))
	}

	// Check DDC bucket output — look for any bucket output that's not StateBucketName
	outputs := tmpl["Outputs"].(map[string]any)
	foundDDCBucket := false
	for name := range outputs {
		if strings.Contains(name, "Bucket") && name != "StateBucketName" {
			foundDDCBucket = true
		}
	}
	if !foundDDCBucket {
		t.Error("expected DDC bucket output")
	}
}

// TestInstanceTypeForModuleDDC verifies DDC instance type resolution.
func TestInstanceTypeForModuleDDC(t *testing.T) {
	cfg := testConfigWithDDC()
	if instanceTypeForModule("ddc", cfg) != "m5.xlarge" {
		t.Errorf("unexpected DDC instance type: %s", instanceTypeForModule("ddc", cfg))
	}

	cfg2 := config.Defaults()
	if instanceTypeForModule("ddc", cfg2) != "m5.xlarge" {
		t.Errorf("unexpected default DDC instance type: %s", instanceTypeForModule("ddc", cfg2))
	}
}

// TestInstanceTypeForModuleWorkstation verifies Workstation instance type resolution.
func TestInstanceTypeForModuleWorkstation(t *testing.T) {
	cfg := testConfigWithWorkstation()
	if instanceTypeForModule("workstation", cfg) != "g4dn.xlarge" {
		t.Errorf("unexpected workstation instance type: %s", instanceTypeForModule("workstation", cfg))
	}

	cfg2 := config.Defaults()
	if instanceTypeForModule("workstation", cfg2) != "g4dn.xlarge" {
		t.Errorf("unexpected default workstation instance type: %s", instanceTypeForModule("workstation", cfg2))
	}
}

// TestSGRulesForModuleDDC verifies DDC SG rules.
func TestSGRulesForModuleDDC(t *testing.T) {
	cfg := testConfigWithDDC()
	rules := sgRulesForModule("ddc", cfg)
	if len(rules) != 2 {
		t.Errorf("expected 2 rules for DDC, got %d", len(rules))
	}
	// Verify correct default ports (80 public, 8080 internal — not 6670/6699).
	if rules[0]["FromPort"] != 80 && rules[0]["FromPort"] != float64(80) {
		t.Errorf("expected public port 80, got %v", rules[0]["FromPort"])
	}
	if rules[1]["FromPort"] != 8080 && rules[1]["FromPort"] != float64(8080) {
		t.Errorf("expected internal port 8080, got %v", rules[1]["FromPort"])
	}
}

// TestSGRulesForModuleWorkstation verifies Workstation SG rules.
func TestSGRulesForModuleWorkstation(t *testing.T) {
	cfg := testConfigWithWorkstation()
	rules := sgRulesForModule("workstation", cfg)
	if len(rules) != 1 {
		t.Errorf("expected 1 rule for workstation, got %d", len(rules))
	}
	// FromPort is an int in the map, but YAML unmarshals to float64
	fromPort := rules[0]["FromPort"]
	if fromPort != 8443 && fromPort != float64(8443) {
		t.Errorf("expected port 8443, got %v (type %T)", fromPort, fromPort)
	}
}

// TestAssumeRolePolicyForModule verifies module-specific trust policies.
func TestAssumeRolePolicyForModule(t *testing.T) {
	// EC2-based modules should use ec2.amazonaws.com
	for _, mod := range []string{"perforce", "ddc", "horde", "lore", "workstation"} {
		policy := assumeRolePolicyForModule(mod)
		stmts, ok := policy["Statement"].([]map[string]any)
		if !ok || len(stmts) == 0 {
			t.Fatalf("module %s: no statements in policy", mod)
		}
		principal, _ := stmts[0]["Principal"].(map[string]any)
		service, _ := principal["Service"].(string)
		if service != "ec2.amazonaws.com" {
			t.Errorf("module %s: expected ec2.amazonaws.com, got %s", mod, service)
		}
	}

	// CI should use codebuild.amazonaws.com
	policy := assumeRolePolicyForModule("ci")
	stmts, _ := policy["Statement"].([]map[string]any)
	principal, _ := stmts[0]["Principal"].(map[string]any)
	service, _ := principal["Service"].(string)
	if service != "codebuild.amazonaws.com" {
		t.Errorf("ci: expected codebuild.amazonaws.com, got %s", service)
	}

	// Deploy should use gamelift.amazonaws.com
	policy = assumeRolePolicyForModule("deploy")
	stmts, _ = policy["Statement"].([]map[string]any)
	principal, _ = stmts[0]["Principal"].(map[string]any)
	service, _ = principal["Service"].(string)
	if service != "gamelift.amazonaws.com" {
		t.Errorf("deploy: expected gamelift.amazonaws.com, got %s", service)
	}
}

// TestManagedPolicyARNsForModule verifies which modules get SSM managed policies.
func TestManagedPolicyARNsForModule(t *testing.T) {
	// EC2-based modules should get SSM policy
	for _, mod := range []string{"perforce", "ddc"} {
		arns := managedPolicyARNsForModule(mod)
		if arns == nil {
			t.Errorf("module %s: expected SSM managed policy ARN", mod)
			continue
		}
		if arns[0]["arn"] != "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore" {
			t.Errorf("module %s: unexpected ARN: %v", mod, arns[0]["arn"])
		}
	}

	// CI and Deploy should NOT get SSM managed policy
	for _, mod := range []string{"ci", "deploy", "horde", "workstation"} {
		arns := managedPolicyARNsForModule(mod)
		if arns != nil {
			t.Errorf("module %s: expected nil managed policy ARNs, got %v", mod, arns)
		}
	}
}

// TestTerraformResourceTypeNewTypes verifies TF resource type mappings for new types.
func TestTerraformResourceTypeNewTypes(t *testing.T) {
	gen := &terraformGenerator{}

	tests := []struct {
		input    string
		expected string
	}{
		{"AWS::CodeBuild::Project", "aws_codebuild_project"},
		{"AWS::GameLift::Alias", "aws_gamelift_alias"},
		{"AWS::GameLift::Fleet", "aws_gamelift_fleet"},
		{"AWS::GameLift::Build", "aws_gamelift_build"},
	}

	for _, tc := range tests {
		got := gen.tfResourceType(tc.input)
		if got != tc.expected {
			t.Errorf("tfResourceType(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

// TestHclCodeBuildEnv verifies CodeBuild environment HCL output.
func TestHclCodeBuildEnv(t *testing.T) {
	gen := &terraformGenerator{}
	env := map[string]any{
		"ComputeType": "BUILD_GENERAL1_SMALL",
		"Image":       "aws/codebuild/standard:7.0",
		"Type":        "LINUX_CONTAINER",
	}
	out := gen.hclCodeBuildEnv(env)
	if !strings.Contains(out, "compute_type") {
		t.Error("expected compute_type in output")
	}
	if !strings.Contains(out, "BUILD_GENERAL1_SMALL") {
		t.Error("expected BUILD_GENERAL1_SMALL in output")
	}
	if !strings.Contains(out, "aws/codebuild/standard:7.0") {
		t.Error("expected image in output")
	}
}

// TestHclCodeBuildArtifacts verifies CodeBuild artifacts HCL output.
func TestHclCodeBuildArtifacts(t *testing.T) {
	gen := &terraformGenerator{}
	art := map[string]any{"Type": "S3"}
	out := gen.hclCodeBuildArtifacts(art)
	if !strings.Contains(out, "artifacts") {
		t.Error("expected artifacts block")
	}
	if !strings.Contains(out, "S3") {
		t.Error("expected S3 type")
	}
}

// TestHclCodeBuildEnvWrongType verifies hclCodeBuildEnv with wrong type.
func TestHclCodeBuildEnvWrongType(t *testing.T) {
	gen := &terraformGenerator{}
	if gen.hclCodeBuildEnv("not a map") != "" {
		t.Error("hclCodeBuildEnv should return empty for wrong type")
	}
}

// TestHclCodeBuildArtifactsWrongType verifies hclCodeBuildArtifacts with wrong type.
func TestHclCodeBuildArtifactsWrongType(t *testing.T) {
	gen := &terraformGenerator{}
	if gen.hclCodeBuildArtifacts("not a map") != "" {
		t.Error("hclCodeBuildArtifacts should return empty for wrong type")
	}
}

// TestCFNVersionV2 verifies CloudFormation metadata version is v2.
func TestCFNVersionV2(t *testing.T) {
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

	metadata := tmpl["Metadata"].(map[string]any)
	fabricaExport := metadata["FabricaExport"].(map[string]any)
	if fabricaExport["Version"] != "v2" {
		t.Errorf("expected version v2, got %v", fabricaExport["Version"])
	}

	// Description should contain V2
	desc := tmpl["Description"].(string)
	if !strings.Contains(desc, "V2") {
		t.Error("expected V2 in description")
	}
}

// TestTFVersionV2 verifies Terraform header version is V2.
func TestTFVersionV2(t *testing.T) {
	st := testStateWithHorde()
	cfg := testConfigWithHorde()

	data, err := GenerateOutput(Terraform, st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := string(data)
	if !strings.Contains(output, "(V2)") {
		t.Error("expected V2 in Terraform header comment")
	}
}

// TestCiProjectNameForModule verifies CI project name resolution.
func TestCiProjectNameForModule(t *testing.T) {
	cfg := testConfigWithCI()
	if ciProjectNameForModule("ci", cfg) != "fabrica-ci" {
		t.Errorf("unexpected CI project name: %s", ciProjectNameForModule("ci", cfg))
	}

	// Default when not set
	cfg2 := config.Defaults()
	if ciProjectNameForModule("ci", cfg2) != "fabrica-ci" {
		t.Errorf("unexpected default CI project name: %s", ciProjectNameForModule("ci", cfg2))
	}
}

// TestCiRoleNameForModule verifies CI role name.
func TestCiRoleNameForModule(t *testing.T) {
	if ciRoleNameForModule("ci") != "fabrica-ci-codebuild" {
		t.Errorf("unexpected CI role name: %s", ciRoleNameForModule("ci"))
	}
}

// TestDeployAliasNameForModule verifies deploy alias name resolution.
func TestDeployAliasNameForModule(t *testing.T) {
	cfg := testConfigWithDeploy()
	if deployAliasNameForModule("deploy", cfg) != "fabrica-deploy" {
		t.Errorf("unexpected deploy alias name: %s", deployAliasNameForModule("deploy", cfg))
	}

	// Default when not set
	cfg2 := config.Defaults()
	if deployAliasNameForModule("deploy", cfg2) != "fabrica-deploy-alias" {
		t.Errorf("unexpected default deploy alias name: %s", deployAliasNameForModule("deploy", cfg2))
	}
}

// TestAmiIDForModuleDDC verifies DDC AMI ID resolution.
func TestAmiIDForModuleDDC(t *testing.T) {
	cfg := testConfigWithDDC()
	if amiIDForModule("ddc", cfg) != "ami-ddc123" {
		t.Errorf("unexpected DDC AMI ID: %s", amiIDForModule("ddc", cfg))
	}
}

// TestAmiIDForModuleWorkstation verifies Workstation AMI ID resolution.
func TestAmiIDForModuleWorkstation(t *testing.T) {
	cfg := testConfigWithWorkstation()
	if amiIDForModule("workstation", cfg) != "ami-ws123" {
		t.Errorf("unexpected Workstation AMI ID: %s", amiIDForModule("workstation", cfg))
	}
}

// TestExportAllV2ModulesInBuildModules verifies all V2 modules are wired into buildModules.
func TestExportAllV2ModulesInBuildModules(t *testing.T) {
	st := state.NewState("123456789012", "us-east-1")
	st.UpsertModule("ddc", "ami", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-ddc", Properties: map[string]string{"instanceType": "m5.xlarge"}},
	})
	st.UpsertModule("workstation", "ami", "ready", []state.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-ws", Properties: map[string]string{"instanceType": "g4dn.xlarge"}},
	})
	st.UpsertModule("ci", "v1", "ready", []state.ModuleResource{
		{TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci", Properties: map[string]string{"Name": "fabrica-ci"}},
	})
	st.UpsertModule("deploy", "v1", "ready", []state.ModuleResource{
		{TypeName: "AWS::GameLift::Alias", Identifier: "alias-1", Properties: map[string]string{"Name": "fabrica-deploy"}},
	})

	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"

	modules, err := buildModules(st, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// state-backend + ddc + workstation + ci + deploy = 5
	if len(modules) != 5 {
		t.Fatalf("expected 5 modules, got %d", len(modules))
	}

	expectedNames := map[string]bool{
		"state-backend": false,
		"ddc":           false,
		"workstation":   false,
		"ci":            false,
		"deploy":        false,
	}
	for _, m := range modules {
		if _, ok := expectedNames[m.Name]; ok {
			expectedNames[m.Name] = true
		}
	}
	for name, found := range expectedNames {
		if !found {
			t.Errorf("expected module %s not found", name)
		}
	}
}

// TestCfPolicyArns verifies CloudFormation policy ARN conversion.
func TestCfPolicyArns(t *testing.T) {
	// Map slice input
	arns := []map[string]any{
		{"arn": "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"},
		{"arn": "arn:aws:iam::aws:policy/ReadOnlyAccess"},
	}
	result := cfPolicyArns(arns)
	if len(result) != 2 {
		t.Fatalf("expected 2 ARNs, got %d", len(result))
	}
	if result[0] != "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore" {
		t.Errorf("unexpected ARN[0]: %s", result[0])
	}

	// String slice input
	strArns := []string{"arn:aws:iam::aws:policy/AdministratorAccess"}
	result = cfPolicyArns(strArns)
	if len(result) != 1 {
		t.Fatalf("expected 1 ARN, got %d", len(result))
	}
	if result[0] != "arn:aws:iam::aws:policy/AdministratorAccess" {
		t.Errorf("unexpected ARN: %s", result[0])
	}

	// Nil input
	result = cfPolicyArns(nil)
	if result != nil {
		t.Errorf("expected nil for nil input, got %v", result)
	}
}
