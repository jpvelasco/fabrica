package export

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/state"
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

func newTestCommand(out *bytes.Buffer, format, outputPath string, st *state.State) command {
	return command{
		format:    format,
		output:    outputPath,
		cfg:       config.Defaults(),
		out:       out,
		readState: func() (*state.State, error) { return st, nil },
	}
}

func TestRunCloudFormation(t *testing.T) {
	var out bytes.Buffer
	st := testStateWithHorde()
	c := newTestCommand(&out, "cloudformation", "", st)
	if err := c.run(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "AWSTemplateFormatVersion") {
		t.Error("expected CloudFormation template format version")
	}
	if !strings.Contains(s, "FabricaStateBucket") {
		t.Error("expected state bucket in output")
	}
}

func TestRunTerraform(t *testing.T) {
	var out bytes.Buffer
	st := testStateWithHorde()
	c := newTestCommand(&out, "terraform", "", st)
	if err := c.run(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, `resource "aws_s3_bucket"`) {
		t.Error("expected S3 bucket resource in Terraform output")
	}
	if !strings.Contains(s, `resource "aws_instance"`) {
		t.Error("expected EC2 instance resource in Terraform output")
	}
}

func TestRunInvalidFormat(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, "invalid", "", testStateWithHorde())
	err := c.run()
	if err == nil {
		t.Fatal("expected error for invalid format")
	}
	if !strings.Contains(err.Error(), "unsupported export format") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRunReadStateError(t *testing.T) {
	var out bytes.Buffer
	c := command{
		format: "cloudformation",
		cfg:    config.Defaults(),
		out:    &out,
		readState: func() (*state.State, error) {
			return nil, errors.New("state file missing")
		},
	}
	err := c.run()
	if err == nil {
		t.Fatal("expected error for readState failure")
	}
	if !strings.Contains(err.Error(), "reading state") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestRunOutputToFile(t *testing.T) {
	dir := t.TempDir()
	outputPath := dir + "/output.yaml"

	var out bytes.Buffer
	st := testStateWithHorde()
	c := newTestCommand(&out, "cloudformation", outputPath, st)
	if err := c.run(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check file was created
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("expected output file to exist: %v", err)
	}
	if !strings.Contains(string(data), "AWSTemplateFormatVersion") {
		t.Error("expected CloudFormation content in output file")
	}

	// Check stdout message
	s := out.String()
	if !strings.Contains(s, "Exported cloudformation template") {
		t.Errorf("expected success message: %s", s)
	}
}

func TestRunEmptyState(t *testing.T) {
	var out bytes.Buffer
	st := state.NewState("", "")
	c := newTestCommand(&out, "cloudformation", "", st)
	err := c.run()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "no modules to export") {
		t.Errorf("expected warning message, got: %s", s)
	}
}

func TestRunNilState(t *testing.T) {
	var out bytes.Buffer
	c := command{
		format: "cloudformation",
		cfg:    config.Defaults(),
		out:    &out,
		readState: func() (*state.State, error) {
			return nil, nil
		},
	}
	err := c.run()
	if err == nil {
		t.Fatal("expected error for nil state")
	}
}

func TestRunOutputToFileError(t *testing.T) {
	// Write to a directory path — should fail
	var out bytes.Buffer
	st := testStateWithHorde()
	c := newTestCommand(&out, "cloudformation", "/nonexistent/dir/output.yaml", st)
	err := c.run()
	if err == nil {
		t.Fatal("expected error for file write failure")
	}
	if !strings.Contains(err.Error(), "writing output file") {
		t.Errorf("unexpected error message: %v", err)
	}
}
