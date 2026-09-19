package create_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/cmd/workstation/create"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(create.New(runtimeSource, optionsSource, out))
	return root
}

func runCreate(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"create"}, args...)...)
}

func newCobraRuntime(provider cloud.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.Workstation.AmiID = "ami-test12345"
	cfg.Workstation.VPCId = "vpc-test"
	cfg.Workstation.SubnetId = "subnet-test"
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

type inspectorProvider struct {
	testutil.TestProvider
	name string
	err  error
}

func (p *inspectorProvider) DescribeImage(_ context.Context, id string) (cloud.ImageInfo, error) {
	if p.err != nil {
		return cloud.ImageInfo{}, p.err
	}
	return cloud.ImageInfo{ID: id, Name: p.name}, nil
}

func TestCreateCobraRejectsStockUbuntuViaInspector(t *testing.T) {
	p := &inspectorProvider{name: "ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-20260826"}
	_, err := runCreate(t, newCobraRuntime(p), "--dry-run")
	if err == nil {
		t.Fatal("expected stock Ubuntu reject")
	}
	testutil.AssertContains(t, err.Error(), "stock Ubuntu")
}

func TestCreateCobraDryRunNoAWSCalls(t *testing.T) {
	provider := &testutil.TestProvider{}
	got, err := runCreate(t, newCobraRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	if provider.CreateCalls != 0 {
		t.Fatalf("dry-run made %d create calls, want 0", provider.CreateCalls)
	}
	testutil.AssertContains(t, got, "dry run")
}

func TestCreateCobraDryRunOutputFields(t *testing.T) {
	provider := &testutil.TestProvider{}
	got, err := runCreate(t, newCobraRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	for _, want := range []string{"123456789012", "us-east-1", "fabrica-workstation-sg", "Cost estimate:"} {
		testutil.AssertContains(t, got, want)
	}
}

func TestCreateCobraYesFlagSkipsConfirmation(t *testing.T) {
	t.Chdir(t.TempDir())
	provider := &testutil.TestProvider{}
	_, err := runCreate(t, newCobraRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("--yes run failed: %v", err)
	}
	if provider.CreateCalls != 4 {
		t.Fatalf("--yes: expected 4 create calls, got %d", provider.CreateCalls)
	}
}

func TestCreateCobraNilProvider(t *testing.T) {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: nil}
	runtimeSource := func() (globals.Runtime, error) { return rt, nil }
	_, err := runCreate(t, runtimeSource)
	if err == nil {
		t.Fatal("expected error when provider is nil")
	}
	testutil.AssertContains(t, err.Error(), "no provider configured")
}

func TestCreateCobraInstanceTypeFlag(t *testing.T) {
	provider := &testutil.TestProvider{}
	got, err := runCreate(t, newCobraRuntime(provider), "--dry-run", "--instance-type", "g5.2xlarge")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "g5.2xlarge")
}

func TestCreateCobraVolumeSizeFlag(t *testing.T) {
	provider := &testutil.TestProvider{}
	got, err := runCreate(t, newCobraRuntime(provider), "--dry-run", "--volume-size", "200")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "200 GiB")
}

func TestCreateCobraAmiIDMissing(t *testing.T) {
	cfg := config.Defaults()
	cfg.Workstation.AmiID = ""
	cfg.Workstation.VPCId = "vpc-test"
	cfg.Workstation.SubnetId = "subnet-test"
	provider := &testutil.TestProvider{}
	rt := globals.Runtime{Config: cfg, Provider: provider}
	runtimeSource := func() (globals.Runtime, error) { return rt, nil }

	_, err := runCreate(t, runtimeSource, "--dry-run")
	if err == nil {
		t.Fatal("expected error when AmiID is missing")
	}
	testutil.AssertContains(t, err.Error(), "workstation.amiId")
}

// TestCreateCobraVPCResolverWiring verifies that workstation create wires the
// provider's VPC resolver, so the default VPC is resolved when config omits
// vpcId/subnetId instead of sending empty values to Cloud Control.
func TestCreateCobraVPCResolverWiring(t *testing.T) {
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.Workstation.AmiID = "ami-test12345"
	provider := &testutil.VPCResolverProvider{VPCID: "vpc-default", SubnetID: "subnet-default"}
	rt := globals.Runtime{Config: cfg, Provider: provider}
	runtimeSource := func() (globals.Runtime, error) { return rt, nil }
	got, err := runCreate(t, runtimeSource, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	testutil.AssertContains(t, got, "vpc-default")
	if provider.Calls != 1 {
		t.Errorf("ResolveDefaultVPC calls = %d, want 1", provider.Calls)
	}
}

// TestCreateCobraExplicitVPCCfgSkipsResolver verifies explicit config
// vpcId/subnetId bypass default-VPC resolution.
func TestCreateCobraExplicitVPCCfgSkipsResolver(t *testing.T) {
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.Workstation.AmiID = "ami-test12345"
	cfg.Workstation.VPCId = "vpc-explicit"
	cfg.Workstation.SubnetId = "subnet-explicit"
	provider := &testutil.VPCResolverProvider{VPCID: "vpc-default", SubnetID: "subnet-default"}
	rt := globals.Runtime{Config: cfg, Provider: provider}
	runtimeSource := func() (globals.Runtime, error) { return rt, nil }
	got, err := runCreate(t, runtimeSource, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	testutil.AssertContains(t, got, "vpc-explicit")
	if provider.Calls != 0 {
		t.Errorf("ResolveDefaultVPC calls = %d, want 0 with explicit config", provider.Calls)
	}
}
