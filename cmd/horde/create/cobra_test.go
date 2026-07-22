package create_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/horde/create"
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

func newCobraTestRuntime(provider cloud.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.State.Table = "fabrica-locks-test"
	cfg.Horde.AmiID = "ami-test123"
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

func newNilProviderRuntime() globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.Horde.AmiID = "ami-test123"
	rt := globals.Runtime{Config: cfg, Provider: nil}
	return func() (globals.Runtime, error) { return rt, nil }
}

// TestCreateCobraDryRunNoAWSCalls verifies --dry-run produces output and no creates.
func TestCreateCobraDryRunNoAWSCalls(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runCreate(t, newCobraTestRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	if provider.createCalls != 0 {
		t.Fatalf("dry-run made %d create calls, want 0", provider.createCalls)
	}
	assertCobraContains(t, got, "dry run")
}

// TestCreateCobraDryRunOutputFields verifies account, region, resource names, cost appear.
func TestCreateCobraDryRunOutputFields(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runCreate(t, newCobraTestRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	for _, want := range []string{
		"123456789012",
		"us-east-1",
		"fabrica-horde-sg",
		"fabrica-horde",
		"Cost estimate:",
	} {
		assertCobraContains(t, got, want)
	}
}

// TestCreateCobraYesFlagSkipsConfirmation verifies --yes executes without prompt.
func TestCreateCobraYesFlagSkipsConfirmation(t *testing.T) {
	t.Chdir(t.TempDir())
	provider := &cobraFakeProvider{}
	_, err := runCreate(t, newCobraTestRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("--yes run failed: %v", err)
	}
	if provider.createCalls != 2 {
		t.Fatalf("--yes: expected 2 create calls, got %d", provider.createCalls)
	}
}

// TestCreateCobraNilProvider verifies nil provider returns a clear error.
func TestCreateCobraNilProvider(t *testing.T) {
	_, err := runCreate(t, newNilProviderRuntime())
	if err == nil {
		t.Fatal("expected error when provider is nil")
	}
	assertCobraContains(t, err.Error(), "no provider configured")
	assertCobraContains(t, err.Error(), "fabrica setup")
}

// TestCreateCobraIdentityFailure verifies identity errors surface as command errors.
func TestCreateCobraIdentityFailure(t *testing.T) {
	provider := &cobraFakeProvider{identityErr: errors.New("credentials unavailable")}
	_, err := runCreate(t, newCobraTestRuntime(provider))
	if err == nil {
		t.Fatal("expected error when identity fails")
	}
	if !cobraContainsString(err.Error(), "resolving identity") {
		t.Fatalf("error %q does not mention resolving identity", err.Error())
	}
}

// TestCreateCobraDryRunInstanceTypeFlag verifies --instance-type appears in dry-run output.
func TestCreateCobraDryRunInstanceTypeFlag(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runCreate(t, newCobraTestRuntime(provider), "--dry-run", "--instance-type", "m7i.4xlarge")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCobraContains(t, got, "m7i.4xlarge")
}

// TestCreateCobraDryRunVolumeSizeFlag verifies --volume-size appears in dry-run output.
func TestCreateCobraDryRunVolumeSizeFlag(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runCreate(t, newCobraTestRuntime(provider), "--dry-run", "--volume-size", "500")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCobraContains(t, got, "500 GiB")
}

// TestCreateCobraAlreadyProvisioned verifies early exit when module is in state.
func TestCreateCobraAlreadyProvisioned(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	stateJSON := `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"horde","version":"","status":"provisioning","resources":[
			{"typeName":"AWS::EC2::SecurityGroup","identifier":"sg-existing"},
			{"typeName":"AWS::EC2::Instance","identifier":"i-existing"}
		]}]}`
	// #nosec G301 -- directory needs execute for traversal
	if err := os.MkdirAll(dir+"/.fabrica", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/.fabrica/state.json", []byte(stateJSON), 0600); err != nil {
		t.Fatal(err)
	}

	provider := &cobraFakeProvider{}
	got, err := runCreate(t, newCobraTestRuntime(provider))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.createCalls != 0 {
		t.Fatalf("already provisioned: made %d create calls, want 0", provider.createCalls)
	}
	assertCobraContains(t, got, "already provisioned")
}

// TestCreateCobraRuntimeError verifies runtimeSource error surfaces as command error.
func TestCreateCobraRuntimeError(t *testing.T) {
	runtimeSource := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not found")
	}
	_, err := runCreate(t, runtimeSource, "--dry-run")
	if err == nil {
		t.Fatal("expected error from runtimeSource")
	}
}

// ---- cobraFakeProvider ----

type cobraFakeProvider struct {
	identityErr error
	createCalls int
}

func (f *cobraFakeProvider) Name() string { return "fake" }

func (f *cobraFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	if f.identityErr != nil {
		return "", "", "", f.identityErr
	}
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *cobraFakeProvider) Resources() cloud.ResourceClient {
	return &cobraFakeResourceClient{provider: f}
}

type cobraFakeResourceClient struct {
	provider *cobraFakeProvider
}

func (r *cobraFakeResourceClient) Create(_ context.Context, res *cloud.Resource) error {
	r.provider.createCalls++
	switch res.TypeName {
	case "AWS::EC2::SecurityGroup":
		res.Identifier = fmt.Sprintf("sg-cobra%04d", r.provider.createCalls)
	case "AWS::EC2::Instance":
		res.Identifier = fmt.Sprintf("i-cobra%04d", r.provider.createCalls)
	}
	return nil
}

func (r *cobraFakeResourceClient) Get(_ context.Context, _ *cloud.Resource) error    { return nil }
func (r *cobraFakeResourceClient) Update(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *cobraFakeResourceClient) Delete(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *cobraFakeResourceClient) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}

func assertCobraContains(t *testing.T, s, substr string) {
	t.Helper()
	if !cobraContainsString(s, substr) {
		t.Fatalf("%q does not contain %q", s, substr)
	}
}

func cobraContainsString(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
