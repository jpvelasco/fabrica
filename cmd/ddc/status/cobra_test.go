package status_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ddc/status"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
)

func TestCobraStatusNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	var buf bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&buf)
	root.AddCommand(status.New(testutil.NewNilProviderRuntime(), optionsSource, &buf))
	if _, err := testutil.RunCommandWithOut(t, root, &buf, "status"); err != nil {
		t.Fatal(err)
	}
}

func TestCobraStatusWithEdges(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	stateJSON := testutil.NewProvisionedStateJSON(testutil.StateModule{
		Name:    "ddc",
		Version: "ami-ddc",
		Status:  "ready",
		Resources: []testutil.StateResource{
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-home"},
			{TypeName: "AWS::EC2::Instance", Identifier: "i-home"},
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-edge", Properties: map[string]any{"region": "eu-west-1", "role": "edge"}},
			{TypeName: "AWS::EC2::Instance", Identifier: "i-edge", Properties: map[string]any{"region": "eu-west-1", "role": "edge", "instanceType": "m7i.large"}},
		},
	})
	testutil.WriteStateFile(t, dir, stateJSON)

	var buf bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&buf)
	root.AddCommand(status.New(testutil.NewTestRuntime(&testutil.TestProvider{}), optionsSource, &buf))
	out, err := testutil.RunCommandWithOut(t, root, &buf, "status")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	// Verify edge region appears in output.
	if !strings.Contains(out, "eu-west-1") {
		t.Fatalf("missing eu-west-1 in output:\n%s", out)
	}
	if !strings.Contains(out, "Edge regions:  1") {
		t.Fatalf("missing edge count in output:\n%s", out)
	}
}

func TestCobraStatusWithEdgesJSON(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	stateJSON := testutil.NewProvisionedStateJSON(testutil.StateModule{
		Name:    "ddc",
		Version: "ami-ddc",
		Status:  "ready",
		Resources: []testutil.StateResource{
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-home"},
			{TypeName: "AWS::EC2::Instance", Identifier: "i-home"},
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-edge", Properties: map[string]any{"region": "eu-west-1", "role": "edge"}},
			{TypeName: "AWS::EC2::Instance", Identifier: "i-edge", Properties: map[string]any{"region": "eu-west-1", "role": "edge", "instanceType": "m7i.large"}},
		},
	})
	testutil.WriteStateFile(t, dir, stateJSON)

	var buf bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&buf)
	root.AddCommand(status.New(testutil.NewTestRuntime(&testutil.TestProvider{}), optionsSource, &buf))
	out, err := testutil.RunCommandWithOut(t, root, &buf, "status", "--json")
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	// Verify JSON structure has edges array.
	if !strings.Contains(out, `"edges"`) {
		t.Fatalf("missing edges in JSON:\n%s", out)
	}
	if !strings.Contains(out, `"region": "eu-west-1"`) {
		t.Fatalf("missing edge region in JSON:\n%s", out)
	}
}
