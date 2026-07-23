package create_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/cmd/perforce/create"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

// buildTestRoot constructs a minimal root command mirroring the production
// persistent-flag hierarchy. --dry-run and --yes live on root.
func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, opts := testutil.BuildTestRoot(out)
	optionsSource := func() globals.Options { return *opts }
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
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

func newNilProviderRuntime() globals.RuntimeSource {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: nil}
	return func() (globals.Runtime, error) { return rt, nil }
}

func TestCreateCobraRuntimeError(t *testing.T) {
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("rt boom")
	}
	_, err := runCreate(t, rt, "--dry-run")
	if err == nil {
		t.Fatal("expected runtime error")
	}
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
	testutil.AssertContains(t, got, "dry run")
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
		"fabrica-perforce-sg",
		"fabrica-perforce",
		"Cost estimate:",
	} {
		testutil.AssertContains(t, got, want)
	}
}

// TestCreateCobraYesFlagSkipsConfirmation verifies --yes executes without prompt.
// Runs in a temp dir so defaultReadState/defaultWriteState find no prior state.
func TestCreateCobraYesFlagSkipsConfirmation(t *testing.T) {
	t.Chdir(t.TempDir())
	provider := &cobraFakeProvider{}
	_, err := runCreate(t, newCobraTestRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("--yes run failed: %v", err)
	}
	if provider.createCalls != 4 {
		t.Fatalf("--yes: expected 4 create calls, got %d", provider.createCalls)
	}
}

// TestCreateCobraVersionLatestAccepted verifies --version latest is valid.
func TestCreateCobraDryRunVersionLatest(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runCreate(t, newCobraTestRuntime(provider), "--dry-run", "--version", "latest")
	if err != nil {
		t.Fatalf("--version latest failed: %v", err)
	}
	testutil.AssertContains(t, got, "latest")
}

// TestCreateCobraVersionInvalidAbortsBeforeAWS verifies invalid version errors early.
func TestCreateCobraVersionInvalid(t *testing.T) {
	provider := &cobraFakeProvider{}
	_, err := runCreate(t, newCobraTestRuntime(provider), "--version", "notaversion")
	if err == nil {
		t.Fatal("expected error for invalid version")
	}
	if provider.createCalls != 0 {
		t.Fatalf("invalid version: made %d create calls, want 0", provider.createCalls)
	}
}

// TestCreateCobraNilProviderExitsCleanly verifies nil provider is handled gracefully.
func TestCreateCobraNilProvider(t *testing.T) {
	_, err := runCreate(t, newNilProviderRuntime())
	if err == nil {
		t.Fatal("expected error when provider is nil")
	}
	testutil.AssertContains(t, err.Error(), "no provider configured")
	testutil.AssertContains(t, err.Error(), "fabrica setup")
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
	got, err := runCreate(t, newCobraTestRuntime(provider), "--dry-run", "--instance-type", "c5.2xlarge")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "c5.2xlarge")
}

// TestCreateCobraDryRunVolumeSizeFlag verifies --volume-size appears in dry-run output.
func TestCreateCobraDryRunVolumeSizeFlag(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runCreate(t, newCobraTestRuntime(provider), "--dry-run", "--volume-size", "1000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "1000 GiB")
}

// TestCreateCobraAlreadyProvisioned verifies early exit when module is in state.
func TestCreateCobraAlreadyProvisioned(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	stateJSON := `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"perforce","version":"2024.2","status":"provisioning","resources":[
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
	testutil.AssertContains(t, got, "already provisioned")
}

// ---- cobraFakeProvider ----

type cobraFakeProvider struct {
	identityErr error
	createCalls int
	amiID       string
	amiErr      error
}

func (f *cobraFakeProvider) Name() string { return "fake" }

func (f *cobraFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	if f.identityErr != nil {
		return "", "", "", f.identityErr
	}
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *cobraFakeProvider) ResolveUbuntuAMI(_ context.Context, _ string) (string, error) {
	if f.amiErr != nil {
		return "", f.amiErr
	}
	if f.amiID == "" {
		return "ami-fake-ubuntu", nil
	}
	return f.amiID, nil
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
	case "AWS::IAM::Role":
		res.Identifier = fmt.Sprintf("role-cobra%04d", r.provider.createCalls)
	case "AWS::IAM::InstanceProfile":
		res.Identifier = "fabrica-perforce-profile"
	}
	return nil
}

func (r *cobraFakeResourceClient) Get(_ context.Context, _ *cloud.Resource) error    { return nil }
func (r *cobraFakeResourceClient) Update(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *cobraFakeResourceClient) Delete(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *cobraFakeResourceClient) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
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
