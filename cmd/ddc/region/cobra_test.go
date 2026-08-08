package region_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ddc/region"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(region.New(runtimeSource, optionsSource, out))
	return root
}

func runRegion(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"region"}, args...)...)
}

// ddcRuntime returns a runtime with a DDC config sufficient for edge plans.
func ddcRuntime(provider cloud.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.DDC = config.DDCConfig{
		AmiID:    "ami-ddc",
		VPCId:    "vpc-1",
		SubnetId: "subnet-1",
	}
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

// stateWithDDC returns a runtime source paired with a state file on disk.
func stateWithDDC(t *testing.T, provider cloud.Provider) (globals.RuntimeSource, string) {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, testutil.NewProvisionedStateJSON(
		testutil.StateModule{
			Name: "ddc", Version: "ami-ddc", Status: "ready",
			Resources: []testutil.StateResource{
				{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coord123"},
				{TypeName: cloud.TypeAWSS3Bucket, Identifier: "b-home123"},
				{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "p-home123"},
				{TypeName: cloud.TypeAWSIAMRole, Identifier: "r-home123"},
				{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-home123"},
			},
		},
	))
	return ddcRuntime(provider), dir
}

func TestRegionCobraParentListsAdd(t *testing.T) {
	got, err := runRegion(t, ddcRuntime(&testutil.TestProvider{}), "--help")
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertContains(t, got, "add")
}

func TestRegionAddCobraDryRun(t *testing.T) {
	rt, _ := stateWithDDC(t, &testutil.TestProvider{})
	got, err := runRegion(t, rt, "add", "eu-west-1", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "dry run")
	testutil.AssertContains(t, got, "eu-west-1")
}

func TestRegionAddCobraFull(t *testing.T) {
	provider := &testutil.TestProvider{}
	rt, _ := stateWithDDC(t, provider)
	got, err := runRegion(t, rt, "add", "eu-west-1", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "provisioned")
	if provider.CreateCalls != 2 {
		t.Errorf("creates = %d, want 2 (SG + instance)", provider.CreateCalls)
	}
}

func TestRegionAddCobraNotProvisioned(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	got, err := runRegion(t, ddcRuntime(&testutil.TestProvider{}), "add", "eu-west-1", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "dry run")
}

func TestRegionAddCobraMissingArg(t *testing.T) {
	_, err := runRegion(t, ddcRuntime(&testutil.TestProvider{}), "add")
	if err == nil {
		t.Fatal("expected usage error for missing REGION")
	}
}

func TestRegionAddCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	_, err := runRegion(t, src, "add", "eu-west-1")
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}
