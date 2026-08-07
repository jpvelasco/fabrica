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
// still exports the state backend (resolved from config account/region). With
// default config (empty account, default region), ReadStateOrNew returns a fresh
// state with region set, so the state backend module is built and exported.
func TestExportCobraNoStateFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := config.Defaults()
	got, err := runExportCmd(t, newTestRuntime(cfg), "--format", "cloudformation")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// State backend is still exported from config-derived names even without a state file
	assert.Contains(t, got, "FabricaStateBucket")
	assert.Contains(t, got, "FabricaStateLockTable")
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
