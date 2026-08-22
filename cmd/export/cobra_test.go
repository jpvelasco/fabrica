package export_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/export"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

// buildTestRoot constructs a minimal root command that mirrors the production
// flag hierarchy: --dry-run, --yes, and --json are persistent flags on root.
func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(export.New(runtimeSource, optionsSource, out))
	return root
}

// runExportCmd builds the command tree, sets args, and executes.
func runExportCmd(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"export"}, args...)...)
}

// newTestRuntime returns a RuntimeSource with default config and nil provider.
func newTestRuntime(cfg *config.Config) globals.RuntimeSource {
	rt := globals.Runtime{Config: cfg, Provider: nil}
	return func() (globals.Runtime, error) { return rt, nil }
}

// testStateWithHorde returns a state with a horde module for export testing.
func testStateWithHorde() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-0abc123def456", "ready", []fabricastate.ModuleResource{
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

// writeStateFile writes state to the standard .fabrica/state.json location.
func writeStateFile(t *testing.T, st *fabricastate.State) {
	t.Helper()
	if err := fabricastate.WriteState(st); err != nil {
		t.Fatal(err)
	}
}

// TestExportCobraCloudFormation verifies --format cloudformation produces valid output.
func TestExportCobraCloudFormation(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, testStateWithHorde())
	cfg := config.Defaults()
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "cloudformation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Contains(t, got, "AWSTemplateFormatVersion")
	assert.Contains(t, got, "FabricaStateBucket")
	assert.Contains(t, got, "HORDESecurityGroupS")
}

// TestExportCobraTerraform verifies --format terraform produces valid output.
func TestExportCobraTerraform(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, testStateWithHorde())
	cfg := config.Defaults()
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "terraform")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Contains(t, got, `resource "aws_s3_bucket"`)
	assert.Contains(t, got, `resource "aws_instance"`)
	assert.Contains(t, got, `resource "aws_security_group"`)
}

// TestExportCobraInvalidFormat verifies unsupported format returns error.
func TestExportCobraInvalidFormat(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, testStateWithHorde())
	cfg := config.Defaults()
	_, err := runExportCmd(t, newTestRuntime(cfg), "--format", "invalid")
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
	if !strings.Contains(err.Error(), "unsupported export format") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestExportCobraMissingFormat verifies missing --format flag errors.
func TestExportCobraMissingFormat(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, testStateWithHorde())
	cfg := config.Defaults()
	_, err := runExportCmd(t, newTestRuntime(cfg))
	if err == nil {
		t.Fatal("expected error for missing required flag")
	}
}

// TestExportCobraOutputToFile verifies --output writes to file.
func TestExportCobraOutputToFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, testStateWithHorde())
	cfg := config.Defaults()
	outputPath := dir + "/output.yaml"
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "cloudformation", "--output", outputPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Contains(t, got, "Exported cloudformation template")
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("expected output file to exist: %v", err)
	}
	if !strings.Contains(string(data), "AWSTemplateFormatVersion") {
		t.Error("expected CloudFormation content in output file")
	}
}

// TestExportCobraDryRunDoesNotWriteFile verifies --dry-run with --output
// previews the plan without writing the file (the example block advertises
// dry-run as a preview; it must not have side effects).
func TestExportCobraDryRunDoesNotWriteFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, testStateWithHorde())
	cfg := config.Defaults()
	outputPath := dir + "/output.yaml"
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "cloudformation", "--output", outputPath, "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Contains(t, got, "Dry run: would write cloudformation template")
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Error("--dry-run must not write the output file")
	}
}

// TestExportCobraDryRunStdoutPreviews verifies --dry-run without --output
// prints a labeled preview to stdout.
func TestExportCobraDryRunStdoutPreviews(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, testStateWithHorde())
	cfg := config.Defaults()
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "cloudformation", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Contains(t, got, "Dry run: template preview follows")
	assert.Contains(t, got, "AWSTemplateFormatVersion")
}

// TestExportCobraEmptyState verifies empty state warns and exits 0.
func TestExportCobraEmptyState(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	st := fabricastate.NewState("", "")
	writeStateFile(t, st)
	cfg := config.Defaults()
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "cloudformation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "no modules to export") {
		t.Errorf("expected warning message, got: %s", got)
	}
}

// TestExportCobraNoStateFile verifies that when no state file exists, the command
// warns and exits 0 instead of exporting anything.
func TestExportCobraNoStateFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := config.Defaults()
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "cloudformation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Contains(t, got, "no state file found")
}

// TestExportCobraRuntimeError verifies runtimeSource errors surface.
func TestExportCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config load failed")
	}
	_, err := runExportCmd(t, src, "--format", "cloudformation")
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// TestExportCobraMultipleModules verifies export with multiple modules.
func TestExportCobraMultipleModules(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-0abc123def456", "ready", []fabricastate.ModuleResource{
		{
			TypeName:   "AWS::EC2::SecurityGroup",
			Identifier: "sg-0horde123456789",
			Properties: map[string]string{"GroupName": "fabrica-horde-sg", "VpcId": "vpc-0abc123"},
		},
		{
			TypeName:   "AWS::EC2::Instance",
			Identifier: "i-0horde123456789",
			Properties: map[string]string{"instanceType": "m7i.2xlarge"},
		},
	})
	st.UpsertModule("perforce", "v23.2", "ready", []fabricastate.ModuleResource{
		{
			TypeName:   "AWS::EC2::SecurityGroup",
			Identifier: "sg-0perf1234567890",
			Properties: map[string]string{"GroupName": "fabrica-perforce-sg", "VpcId": "vpc-0abc123"},
		},
		{
			TypeName:   "AWS::EC2::Instance",
			Identifier: "i-0perf1234567890",
			Properties: map[string]string{"instanceType": "c5.2xlarge"},
		},
	})
	writeStateFile(t, st)
	cfg := config.Defaults()
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "cloudformation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Contains(t, got, "HORDESecurityGroupS")
	assert.Contains(t, got, "PERFORCESecurityGroupS")
	assert.Contains(t, got, "FabricaStateBucket")
}

// TestExportCobraStateBackend verifies state backend resources are always exported.
func TestExportCobraStateBackend(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, testStateWithHorde())
	cfg := config.Defaults()
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "cloudformation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Contains(t, got, "FabricaStateBucket")
	assert.Contains(t, got, "FabricaStateLockTable")
}

// TestExportCobraTerraformProvider verifies terraform provider block.
func TestExportCobraTerraformProvider(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, testStateWithHorde())
	cfg := config.Defaults()
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "terraform")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Contains(t, got, `terraform {`)
	assert.Contains(t, got, `required_providers`)
	assert.Contains(t, got, `aws`)
}

// TestExportCobraCloudFormationOutputs verifies CloudFormation Outputs section.
func TestExportCobraCloudFormationOutputs(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, testStateWithHorde())
	cfg := config.Defaults()
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "cloudformation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Contains(t, got, "Outputs")
}

// TestExportCobraHelp verifies help output.
func TestExportCobraHelp(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := config.Defaults()
	var out bytes.Buffer
	root := buildTestRoot(newTestRuntime(cfg), &out)
	root.SetArgs([]string{"export", "--help"})
	_ = root.ExecuteContext(context.Background())
	output := out.String()
	assert.Contains(t, output, "Generate infrastructure-as-code templates")
	assert.Contains(t, output, "--format")
	assert.Contains(t, output, "--output")
}

// TestExportCobraYAMLParse verifies CloudFormation output structure.
func TestExportCobraYAMLParse(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, testStateWithHorde())
	cfg := config.Defaults()
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "cloudformation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify it contains expected CloudFormation structure
	var raw map[string]any
	if err := json.Unmarshal([]byte(got), &raw); err != nil {
		// YAML output won't parse as JSON, but we can check structure
		// The real validation is that it contains the expected CloudFormation keys
		if !strings.Contains(got, "AWSTemplateFormatVersion") {
			t.Error("expected AWSTemplateFormatVersion in output")
		}
		if !strings.Contains(got, "Resources") {
			t.Error("expected Resources in output")
		}
	}
}

// ---- V2 module cobra tests ----

// testStateWithDDC returns a state with a DDC module for export testing.
func testStateWithDDC() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("ddc", "ami-ddc123", "ready", []fabricastate.ModuleResource{
		{
			TypeName:   "AWS::EC2::SecurityGroup",
			Identifier: "sg-ddc123",
			Properties: map[string]string{"GroupName": "fabrica-ddc-sg", "VpcId": "vpc-0abc123"},
		},
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
	return st
}

// testStateWithCI returns a state with a CI module for export testing.
func testStateWithCI() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "fabrica-ci", "ready", []fabricastate.ModuleResource{
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
	return st
}

// testStateWithDeploy returns a state with a Deploy module for export testing.
func testStateWithDeploy() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("deploy", "v1.0.0", "ready", []fabricastate.ModuleResource{
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
	return st
}

// TestExportCobraDDC verifies DDC module exports via CLI.
func TestExportCobraDDC(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, testStateWithDDC())
	cfg := config.Defaults()
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "cloudformation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have DDC resources, not only state backend
	assert.Contains(t, got, "DDC")
	assert.Contains(t, got, "FabricaStateBucket")
}

// TestExportCobraDDCTerraform verifies DDC module exports as Terraform.
func TestExportCobraDDCTerraform(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, testStateWithDDC())
	cfg := config.Defaults()
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "terraform")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Contains(t, got, `resource "aws_s3_bucket"`)
	assert.Contains(t, got, `resource "aws_instance"`)
	assert.Contains(t, got, `resource "aws_security_group"`)
}

// TestExportCobraCI verifies CI module exports via CLI.
func TestExportCobraCI(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, testStateWithCI())
	cfg := config.Defaults()
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "cloudformation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Contains(t, got, "CodeBuild")
	assert.Contains(t, got, "FabricaStateBucket")
}

// TestExportCobraCITerraform verifies CI module exports as Terraform.
func TestExportCobraCITerraform(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, testStateWithCI())
	cfg := config.Defaults()
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "terraform")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Contains(t, got, `resource "aws_codebuild_project"`)
	assert.Contains(t, got, `resource "aws_iam_role"`)
}

// TestExportCobraDeploy verifies Deploy module exports via CLI.
func TestExportCobraDeploy(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, testStateWithDeploy())
	cfg := config.Defaults()
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "cloudformation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Contains(t, got, "GameLift")
	assert.Contains(t, got, "FabricaStateBucket")
}

// TestExportCobraDeployTerraform verifies Deploy module exports as Terraform.
func TestExportCobraDeployTerraform(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, testStateWithDeploy())
	cfg := config.Defaults()
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "terraform")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assert.Contains(t, got, `resource "aws_gamelift_alias"`)
	assert.Contains(t, got, `resource "aws_gamelift_fleet"`)
}

// TestExportCobraMixedV1V2 verifies mixed V1+V2 modules export correctly.
func TestExportCobraMixedV1V2(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("horde", "ami-0abc123def456", "ready", []fabricastate.ModuleResource{
		{
			TypeName:   "AWS::EC2::Instance",
			Identifier: "i-0abc123def456",
			Properties: map[string]string{"instanceType": "m7i.2xlarge"},
		},
	})
	st.UpsertModule("ddc", "ami-ddc", "ready", []fabricastate.ModuleResource{
		{
			TypeName:   "AWS::EC2::Instance",
			Identifier: "i-ddc",
			Properties: map[string]string{"instanceType": "m5.xlarge"},
		},
		{
			TypeName:   "AWS::S3::Bucket",
			Identifier: "fabrica-ddc-bucket-123",
			Properties: map[string]string{"BucketName": "fabrica-ddc-bucket-123"},
		},
	})
	writeStateFile(t, st)
	cfg := config.Defaults()
	cfg.Horde.AmiID = "ami-0abc123def456"
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "cloudformation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// V1 resources
	assert.Contains(t, got, "HORDE")
	// V2 resources
	assert.Contains(t, got, "DDC")
	// State backend
	assert.Contains(t, got, "FabricaStateBucket")
}
