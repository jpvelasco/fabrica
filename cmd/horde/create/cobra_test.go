package create_test

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/horde/create"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
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
	provider := &testutil.TestProvider{}
	got, err := runCreate(t, newCobraTestRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v", err)
	}
	if provider.CreateCalls != 0 {
		t.Fatalf("dry-run made %d create calls, want 0", provider.CreateCalls)
	}
	testutil.AssertContains(t, got, "dry run")
}

// TestCreateCobraDryRunOutputFields verifies account, region, resource names, cost appear.
func TestCreateCobraDryRunOutputFields(t *testing.T) {
	provider := &testutil.TestProvider{}
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
		testutil.AssertContains(t, got, want)
	}
}

// TestCreateCobraYesFlagSkipsConfirmation verifies --yes executes without prompt.
func TestCreateCobraYesFlagSkipsConfirmation(t *testing.T) {
	t.Chdir(t.TempDir())
	provider := &testutil.TestProvider{}
	_, err := runCreate(t, newCobraTestRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("--yes run failed: %v", err)
	}
	if provider.CreateCalls != 4 {
		t.Fatalf("--yes: expected 4 create calls (SG, role, profile, instance), got %d", provider.CreateCalls)
	}
}

// TestCreateCobraNilProvider verifies nil provider returns a clear error.
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
	provider := &testutil.TestProvider{IdentityErr: errors.New("credentials unavailable")}
	_, err := runCreate(t, newCobraTestRuntime(provider))
	if err == nil {
		t.Fatal("expected error when identity fails")
	}
	testutil.AssertContains(t, err.Error(), "could not resolve AWS identity")
}

// TestCreateCobraDryRunInstanceTypeFlag verifies --instance-type appears in dry-run output.
func TestCreateCobraDryRunInstanceTypeFlag(t *testing.T) {
	provider := &testutil.TestProvider{}
	got, err := runCreate(t, newCobraTestRuntime(provider), "--dry-run", "--instance-type", "m7i.4xlarge")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "m7i.4xlarge")
}

// TestCreateCobraDryRunVolumeSizeFlag verifies --volume-size appears in dry-run output.
func TestCreateCobraDryRunVolumeSizeFlag(t *testing.T) {
	provider := &testutil.TestProvider{}
	got, err := runCreate(t, newCobraTestRuntime(provider), "--dry-run", "--volume-size", "500")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "500 GiB")
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

	provider := &testutil.TestProvider{}
	got, err := runCreate(t, newCobraTestRuntime(provider))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.CreateCalls != 0 {
		t.Fatalf("already provisioned: made %d create calls, want 0", provider.CreateCalls)
	}
	testutil.AssertContains(t, got, "already provisioned")
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
