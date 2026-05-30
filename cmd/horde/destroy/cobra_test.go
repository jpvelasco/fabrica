package destroy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/horde/destroy"
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
	root.PersistentFlags().BoolVarP(&opts.JSONOutput, "json", "j", false, "")
	root.SetOut(out)
	root.SetErr(out)

	optionsSource := func() globals.Options { return opts }
	root.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return root
}

func runDestroy(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"destroy"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func newRuntime(provider cloud.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

func newNilProviderRuntime() globals.RuntimeSource {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: nil}
	return func() (globals.Runtime, error) { return rt, nil }
}

// TestDestroyCobraNotProvisioned verifies clean message when no state on disk.
func TestDestroyCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runDestroy(t, newRuntime(&cobraFakeProvider{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCobraContains(t, got, "not provisioned")
}

// TestDestroyCobraDryRunNoDeleteCalls verifies --dry-run produces output without calling delete.
func TestDestroyCobraDryRunNoDeleteCalls(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, provisionedStateJSON())

	provider := &cobraFakeProvider{}
	got, err := runDestroy(t, newRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCobraContains(t, got, "dry run")
	if provider.deleteCalls != 0 {
		t.Errorf("dry-run made %d delete calls, want 0", provider.deleteCalls)
	}
}

// TestDestroyCobraDryRunShowsResources verifies resource IDs appear in dry-run output.
func TestDestroyCobraDryRunShowsResources(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, provisionedStateJSON())

	got, err := runDestroy(t, newRuntime(&cobraFakeProvider{}), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCobraContains(t, got, "i-cobra123")
	assertCobraContains(t, got, "sg-cobra123")
}

// TestDestroyCobraYesFlagDestroysResources verifies --yes destroys without prompt.
func TestDestroyCobraYesFlagDestroysResources(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, provisionedStateJSON())

	provider := &cobraFakeProvider{}
	got, err := runDestroy(t, newRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.deleteCalls != 2 {
		t.Errorf("expected 2 delete calls, got %d", provider.deleteCalls)
	}
	assertCobraContains(t, got, "destroyed")
}

// TestDestroyCobraJSONNotProvisioned verifies --json output when not provisioned.
func TestDestroyCobraJSONNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runDestroy(t, newRuntime(&cobraFakeProvider{}), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result destroy.DestroyOutput
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if len(result.Destroyed) != 0 {
		t.Errorf("destroyed = %v, want empty", result.Destroyed)
	}
}

// TestDestroyCobraJSONDryRun verifies --json --dry-run outputs valid JSON with dryRun=true.
func TestDestroyCobraJSONDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, provisionedStateJSON())

	got, err := runDestroy(t, newRuntime(&cobraFakeProvider{}), "--json", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result destroy.DestroyOutput
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if !result.DryRun {
		t.Error("dryRun must be true")
	}
	if len(result.Destroyed) != 2 {
		t.Errorf("expected 2 in destroyed list for dry run, got %d", len(result.Destroyed))
	}
}

// TestDestroyCobraJSONYes verifies --json --yes output after successful destroy.
func TestDestroyCobraJSONYes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, provisionedStateJSON())

	got, err := runDestroy(t, newRuntime(&cobraFakeProvider{}), "--json", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result destroy.DestroyOutput
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if result.DryRun {
		t.Error("dryRun must be false")
	}
	if len(result.Destroyed) != 2 {
		t.Errorf("expected 2 destroyed, got %d: %v", len(result.Destroyed), result.Destroyed)
	}
}

// TestDestroyCobraNilProviderNoState verifies nil provider with no state exits cleanly.
func TestDestroyCobraNilProvider(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runDestroy(t, newNilProviderRuntime())
	if err != nil {
		t.Fatalf("nil provider: unexpected error: %v", err)
	}
	assertCobraContains(t, got, "not provisioned")
}

// TestDestroyCobraRuntimeError verifies runtimeSource errors surface as command errors.
func TestDestroyCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	_, err := runDestroy(t, src)
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// ---- helpers ----

func provisionedStateJSON() string {
	return `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"horde","version":"ami-0abc123def456789","status":"provisioning","resources":[
			{"typeName":"AWS::EC2::SecurityGroup","identifier":"sg-cobra123"},
			{"typeName":"AWS::EC2::Instance","identifier":"i-cobra123"}
		]}]}`
}

func writeStateFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir+"/.fabrica", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/.fabrica/state.json", []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
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

// ---- cobraFakeProvider ----

type cobraFakeProvider struct {
	deleteCalls int
}

func (f *cobraFakeProvider) Name() string { return "fake" }

func (f *cobraFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *cobraFakeProvider) Resources() cloud.ResourceClient {
	return &cobraFakeRC{provider: f}
}

type cobraFakeRC struct {
	provider *cobraFakeProvider
}

func (r *cobraFakeRC) Create(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *cobraFakeRC) Get(_ context.Context, _ *cloud.Resource) error    { return nil }
func (r *cobraFakeRC) Update(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *cobraFakeRC) Delete(_ context.Context, _ *cloud.Resource) error {
	r.provider.deleteCalls++
	return nil
}
func (r *cobraFakeRC) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}
