package destroy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ddc/destroy"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
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
	return testutil.NewTestRuntime(provider)
}

func newNilProviderRuntime() globals.RuntimeSource {
	return testutil.NewNilProviderRuntime()
}

// TestDestroyCobraNotProvisioned verifies clean message when no state on disk.
func TestDestroyCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runDestroy(t, newRuntime(&testutil.CobraFakeProvider{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
}

// TestDestroyCobraDryRunNoDeleteCalls verifies --dry-run produces output without calling delete.
func TestDestroyCobraDryRunNoDeleteCalls(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())

	provider := &testutil.CobraFakeProvider{}
	got, err := runDestroy(t, newRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "dry run")
	if provider.DeleteCalls != 0 {
		t.Errorf("dry-run made %d delete calls, want 0", provider.DeleteCalls)
	}
}

// TestDestroyCobraDryRunShowsResources verifies resource IDs appear in dry-run output.
func TestDestroyCobraDryRunShowsResources(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())

	got, err := runDestroy(t, newRuntime(&testutil.CobraFakeProvider{}), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "i-coord123")
	testutil.AssertContains(t, got, "sg-ddc123")
}

// TestDestroyCobraYesFlagDestroysResources verifies --yes destroys without prompt.
func TestDestroyCobraYesFlagDestroysResources(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())

	provider := &testutil.CobraFakeProvider{}
	got, err := runDestroy(t, newRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Coordinator, scylla, bucket, profile, role, SG = 6 resources
	if provider.DeleteCalls != 6 {
		t.Errorf("expected 6 delete calls, got %d", provider.DeleteCalls)
	}
	testutil.AssertContains(t, got, "destroyed")
}

// TestDestroyCobraJSONNotProvisioned verifies --json output when not provisioned.
func TestDestroyCobraJSONNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runDestroy(t, newRuntime(&testutil.CobraFakeProvider{}), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result teardown.Output
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
	testutil.WriteStateFile(t, dir, provisionedStateJSON())

	got, err := runDestroy(t, newRuntime(&testutil.CobraFakeProvider{}), "--json", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result teardown.Output
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if !result.DryRun {
		t.Error("dryRun must be true")
	}
	if len(result.Destroyed) != 6 {
		t.Errorf("expected 6 in destroyed list for dry run, got %d", len(result.Destroyed))
	}
}

// TestDestroyCobraJSONYes verifies --json --yes output after successful destroy.
func TestDestroyCobraJSONYes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())

	got, err := runDestroy(t, newRuntime(&testutil.CobraFakeProvider{}), "--json", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result teardown.Output
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if result.DryRun {
		t.Error("dryRun must be false")
	}
	if len(result.Destroyed) != 6 {
		t.Errorf("expected 6 destroyed, got %d: %v", len(result.Destroyed), result.Destroyed)
	}
}

// TestDestroyCobraNilProviderNoState verifies nil provider with no state exits cleanly.
func TestDestroyCobraNilProvider(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runDestroy(t, newNilProviderRuntime())
	if err != nil {
		t.Fatalf("nil provider: unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
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

// provisionedStateJSON returns a DDC state with coordinator, scylla, bucket, profile, role, SG.
func provisionedStateJSON() string {
	return `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"ddc","version":"ami-ddc123","status":"ready","resources":[
			{"typeName":"AWS::EC2::Instance","identifier":"i-coord123","properties":{"role":"coordinator"}},
			{"typeName":"AWS::EC2::Instance","identifier":"i-scylla123","properties":{"role":"scylla"}},
			{"typeName":"AWS::S3::Bucket","identifier":"ddc-bucket-123"},
			{"typeName":"AWS::IAM::InstanceProfile","identifier":"ddc-profile"},
			{"typeName":"AWS::IAM::Role","identifier":"ddc-role"},
			{"typeName":"AWS::EC2::SecurityGroup","identifier":"sg-ddc123"}
		]}]}`
}

// TestNewTeardownWiring verifies NewTeardown returns a Command with correct wiring.
func TestNewTeardownWiring(t *testing.T) {
	var out bytes.Buffer
	rt := globals.Runtime{Config: config.Defaults(), Provider: &testutil.CobraFakeProvider{}}
	tc := destroy.NewTeardown(rt, &out)
	if !tc.SkipConfirm || !tc.AssumeYes {
		t.Fatalf("SkipConfirm/AssumeYes must be true; got SkipConfirm=%v, AssumeYes=%v", tc.SkipConfirm, tc.AssumeYes)
	}
	if tc.ReadState == nil || tc.WriteState == nil || tc.DeleteResource == nil || tc.GetResource == nil {
		t.Fatal("provider seams must be wired when provider is non-nil")
	}
	if tc.Runtime.Provider == nil {
		t.Fatal("Runtime.Provider must be set")
	}
	if tc.Spec.ModuleName != "ddc" {
		t.Errorf("module name = %q, want ddc", tc.Spec.ModuleName)
	}
}

// TestNewTeardownNilProvider verifies NewTeardown handles nil provider gracefully.
func TestNewTeardownNilProvider(t *testing.T) {
	var out bytes.Buffer
	rt := globals.Runtime{Config: config.Defaults(), Provider: nil}
	tc := destroy.NewTeardown(rt, &out)
	if !tc.SkipConfirm || !tc.AssumeYes {
		t.Fatal("SkipConfirm/AssumeYes must be true even with nil provider")
	}
	if tc.ReadState == nil || tc.WriteState == nil {
		t.Fatal("ReadState and WriteState must always be wired")
	}
	// With nil provider, Delete/Get seams stay nil.
	if tc.DeleteResource != nil || tc.GetResource != nil {
		t.Fatal("DeleteResource/GetResource must be nil when provider is nil")
	}
}

// TestResourceOrder verifies deletion order: coordinator → scylla → bucket → profile → role → SG.
func TestResourceOrder(t *testing.T) {
	m := &fabricastate.ModuleState{
		Name: "ddc",
		Resources: []fabricastate.ModuleResource{
			{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-ddc123"},
			{TypeName: "AWS::IAM::Role", Identifier: "ddc-role"},
			{TypeName: "AWS::IAM::InstanceProfile", Identifier: "ddc-profile"},
			{TypeName: "AWS::S3::Bucket", Identifier: "ddc-bucket"},
			{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-scylla123", Properties: map[string]string{"role": "scylla"}},
			{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coord123", Properties: map[string]string{"role": "coordinator"}},
		},
	}
	order := destroy.ResourceOrder(m)
	if len(order) != 6 {
		t.Fatalf("expected 6 resources, got %d", len(order))
	}
	// Verify order: coordinator → scylla → bucket → profile → role → SG
	expected := []string{
		"i-coord123", "i-scylla123", "ddc-bucket", "ddc-profile", "ddc-role", "sg-ddc123",
	}
	for i, want := range expected {
		if order[i].Identifier != want {
			t.Errorf("order[%d] = %q, want %q", i, order[i].Identifier, want)
		}
	}
}
