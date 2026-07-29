package destroy_test

import (
	"bytes"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/deploy/destroy"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return root
}

func runDestroy(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"destroy"}, args...)...)
}

// TestDestroyCobraNotProvisioned verifies clean message when deploy is not provisioned.
func TestDestroyCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.TestProvider{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
}

// TestDestroyCobraDryRun verifies --dry-run shows the plan without deleting.
func TestDestroyCobraDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, deployStateJSON())

	provider := &testutil.TestProvider{}
	got, err := runDestroy(t, testutil.NewTestRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "dry run")
	if provider.DeleteCalls > 0 {
		t.Errorf("--dry-run should not make delete calls, made %d", provider.DeleteCalls)
	}
}

// TestNewTeardownWiring verifies NewTeardown returns a Command with correct wiring for deploy.
func TestNewTeardownWiring(t *testing.T) {
	var out bytes.Buffer
	rt := globals.Runtime{Config: config.Defaults(), Provider: &testutil.TestProvider{}}
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
	if tc.Spec.ModuleName != "deploy" {
		t.Errorf("module name = %q, want deploy", tc.Spec.ModuleName)
	}
}

// ---- helpers ----

func deployStateJSON() string {
	return testutil.NewProvisionedStateJSON(testutil.StateModule{
		Name: "deploy", Version: "v1.0.0", Status: "ready",
		Resources: []testutil.StateResource{
			{TypeName: "AWS::GameLift::Fleet", Identifier: "fleet-1"},
			{TypeName: "AWS::GameLift::Build", Identifier: "build-1"},
			{TypeName: "AWS::GameLift::Alias", Identifier: "alias-1"},
			{TypeName: "AWS::IAM::Role", Identifier: "role-1"},
		},
	})
}
