package create_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/workstation/create"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	var opts globals.Options
	root := &cobra.Command{
		Use:           "fabrica",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "")
	root.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "")
	root.SetOut(out)
	root.SetErr(out)
	optionsSource := func() globals.Options { return opts }
	root.AddCommand(create.New(runtimeSource, optionsSource, out))
	return root
}

func runCreate(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"create"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
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

func TestCreateCobraDryRunNoAWSCalls(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runCreate(t, newCobraRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	if provider.createCalls != 0 {
		t.Fatalf("dry-run made %d create calls, want 0", provider.createCalls)
	}
	assertCobraContains(t, got, "dry run")
}

func TestCreateCobraDryRunOutputFields(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runCreate(t, newCobraRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	for _, want := range []string{"123456789012", "us-east-1", "fabrica-workstation-sg", "Cost estimate:"} {
		assertCobraContains(t, got, want)
	}
}

func TestCreateCobraYesFlagSkipsConfirmation(t *testing.T) {
	t.Chdir(t.TempDir())
	provider := &cobraFakeProvider{}
	_, err := runCreate(t, newCobraRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("--yes run failed: %v", err)
	}
	if provider.createCalls != 2 {
		t.Fatalf("--yes: expected 2 create calls, got %d", provider.createCalls)
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
	assertCobraContains(t, err.Error(), "no provider configured")
}

func TestCreateCobraInstanceTypeFlag(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runCreate(t, newCobraRuntime(provider), "--dry-run", "--instance-type", "g5.2xlarge")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCobraContains(t, got, "g5.2xlarge")
}

func TestCreateCobraVolumeSizeFlag(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runCreate(t, newCobraRuntime(provider), "--dry-run", "--volume-size", "200")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCobraContains(t, got, "200 GiB")
}

func TestCreateCobraAmiIDMissing(t *testing.T) {
	cfg := config.Defaults()
	cfg.Workstation.AmiID = ""
	cfg.Workstation.VPCId = "vpc-test"
	cfg.Workstation.SubnetId = "subnet-test"
	provider := &cobraFakeProvider{}
	rt := globals.Runtime{Config: cfg, Provider: provider}
	runtimeSource := func() (globals.Runtime, error) { return rt, nil }

	_, err := runCreate(t, runtimeSource, "--dry-run")
	if err == nil {
		t.Fatal("expected error when AmiID is missing")
	}
	assertCobraContains(t, err.Error(), "workstation.amiId")
}

type cobraFakeProvider struct {
	createCalls int
}

func (f *cobraFakeProvider) Name() string { return "fake" }
func (f *cobraFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}
func (f *cobraFakeProvider) Resources() cloud.ResourceClient {
	return &cobraFakeRC{provider: f}
}

type cobraFakeRC struct{ provider *cobraFakeProvider }

func (r *cobraFakeRC) Create(_ context.Context, res *cloud.Resource) error {
	r.provider.createCalls++
	switch res.TypeName {
	case "AWS::EC2::SecurityGroup":
		res.Identifier = fmt.Sprintf("sg-cobra%04d", r.provider.createCalls)
	case "AWS::EC2::Instance":
		res.Identifier = fmt.Sprintf("i-cobra%04d", r.provider.createCalls)
	}
	return nil
}
func (r *cobraFakeRC) Get(_ context.Context, _ *cloud.Resource) error    { return nil }
func (r *cobraFakeRC) Update(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *cobraFakeRC) Delete(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *cobraFakeRC) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}

func assertCobraContains(t *testing.T, s, substr string) {
	t.Helper()
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Fatalf("%q does not contain %q", s, substr)
}
