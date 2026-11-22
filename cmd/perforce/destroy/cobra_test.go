package destroy_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/cmd/perforce/destroy"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return root
}

func TestDestroyHelpDocumentsIAM(t *testing.T) {
	var out bytes.Buffer
	cmd := destroy.New(testutil.NewTestRuntime(&testutil.TestProvider{}), func() globals.Options { return globals.Options{} }, &out)
	testutil.AssertLongHelpMatches(t, cmd.Long,
		"IAM Instance Profile",
		"IAM Role",
		"EC2 Security Group",
		"data volume is retained",
	)
}

func runDestroy(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"destroy"}, args...)...)
}

// TestDestroyCobraNotProvisioned verifies clean message when no state on disk.
func TestDestroyCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.TestProvider{}))
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

	provider := &testutil.TestProvider{}
	got, err := runDestroy(t, testutil.NewTestRuntime(provider), "--dry-run")
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

	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.TestProvider{}), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "i-cobra123")
	testutil.AssertContains(t, got, "sg-cobra123")
}

// TestDestroyCobraYesFlagDestroysResources verifies --yes destroys without prompt.
func TestDestroyCobraYesFlagDestroysResources(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())

	provider := &testutil.TestProvider{}
	got, err := runDestroy(t, testutil.NewTestRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.DeleteCalls != 2 {
		t.Errorf("expected 2 delete calls, got %d", provider.DeleteCalls)
	}
	testutil.AssertContains(t, got, "destroyed")
}

// TestDestroyCobraJSONNotProvisioned verifies --json output when not provisioned.
func TestDestroyCobraJSONNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.TestProvider{}), "--json")
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

	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.TestProvider{}), "--json", "--dry-run")
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
	if len(result.Destroyed) != 2 {
		t.Errorf("expected 2 in destroyed list for dry run, got %d", len(result.Destroyed))
	}
}

// TestDestroyCobraJSONYes verifies --json --yes output after successful destroy.
func TestDestroyCobraJSONYes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())

	got, err := runDestroy(t, testutil.NewTestRuntime(&testutil.TestProvider{}), "--json", "--yes")
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
	if len(result.Destroyed) != 2 {
		t.Errorf("expected 2 destroyed, got %d: %v", len(result.Destroyed), result.Destroyed)
	}
}

// TestDestroyCobraNilProviderNoState verifies nil provider with no state exits cleanly.
func TestDestroyCobraNilProvider(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runDestroy(t, testutil.NewNilProviderRuntime())
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

func provisionedStateJSON() string {
	return testutil.NewProvisionedStateJSON(testutil.StateModule{
		Name: "perforce", Version: "2024.2", Status: "provisioning",
		Resources: testutil.EC2Pair("sg-cobra123", "i-cobra123"),
	})
}

// TestNewTeardownWiring verifies NewTeardown returns a Command with correct wiring.
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
	if tc.Spec.ModuleName != "perforce" {
		t.Errorf("module name = %q, want perforce", tc.Spec.ModuleName)
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

// testAdminParam is a well-formed per-create admin-password name (#700).
const testAdminParam = "/fabrica/perforce/0123456789abcdef0123456789abcdef/admin-password"

func adminPassStateJSON(param string) string {
	props := map[string]any{}
	if param != "" {
		props["adminPasswordParameter"] = param
	}
	return testutil.NewProvisionedStateJSON(testutil.StateModule{
		Name: "perforce", Version: "2025.2", Status: "ready",
		Resources: []testutil.StateResource{
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-cobra123"},
			{TypeName: "AWS::EC2::Instance", Identifier: "i-cobra123", Properties: props},
		},
	})
}

// TestDestroyCobraDeletesAdminPasswordBeforeInstance pins #700's teardown:
// the SecureString is deleted, and only then the instance, so a failed or
// interrupted destroy cannot orphan it. A failed instance delete leaves the
// parameter already gone and the instance record (with its name) in state.
func TestDestroyCobraDeletesAdminPasswordBeforeInstance(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, adminPassStateJSON(testAdminParam))

	failing := &testutil.TestProvider{DeleteErr: map[string]error{cloud.TypeAWSEC2Instance: errors.New("instance busy")}}
	if _, err := runDestroy(t, testutil.NewTestRuntime(failing), "--yes"); err == nil {
		t.Fatal("expected destroy to fail on the instance delete")
	}
	if len(failing.DeletedParameters) != 1 || failing.DeletedParameters[0] != testAdminParam {
		t.Fatalf("DeletedParameters = %v, want the admin password deleted before the instance", failing.DeletedParameters)
	}
	m := readPerforceModule(t, dir)
	inst, ok := stateutil.ResourceByType(m, cloud.TypeAWSEC2Instance)
	if !ok || inst.Properties["adminPasswordParameter"] != testAdminParam {
		t.Fatal("instance record with its adminPasswordParameter must survive a failed destroy so a retry can find the name")
	}
}

func TestDestroyCobraAdminPasswordDeleteFailureKeepsState(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, adminPassStateJSON(testAdminParam))

	failing := &testutil.TestProvider{DeleteParameterErr: errors.New("ssm unavailable")}
	_, err := runDestroy(t, testutil.NewTestRuntime(failing), "--yes")
	if err == nil || !strings.Contains(err.Error(), testAdminParam) {
		t.Fatalf("err = %v, want it to name the failing parameter", err)
	}
	if failing.DeleteCalls != 0 {
		t.Errorf("resource deletes = %d, want 0 (the failed parameter delete must abort first)", failing.DeleteCalls)
	}
}

// TestDestroyCobraSkipsAdminPasswordWhenNotRecorded covers a pre-#700 instance:
// no name is recorded, so there is nothing to delete and destroy still succeeds.
func TestDestroyCobraSkipsAdminPasswordWhenNotRecorded(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, adminPassStateJSON(""))

	provider := &testutil.TestProvider{}
	if _, err := runDestroy(t, testutil.NewTestRuntime(provider), "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(provider.DeletedParameters) != 0 {
		t.Errorf("DeletedParameters = %v, want none for a pre-#700 instance", provider.DeletedParameters)
	}
}

// TestDestroyCobraSkipsMalformedAdminPasswordName keeps a hand-edited state
// entry from widening the delete target to an arbitrary SSM parameter.
func TestDestroyCobraSkipsMalformedAdminPasswordName(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, adminPassStateJSON("/fabrica/workstation/0123456789abcdef0123456789abcdef/dcv-password"))

	provider := &testutil.TestProvider{}
	if _, err := runDestroy(t, testutil.NewTestRuntime(provider), "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(provider.DeletedParameters) != 0 {
		t.Errorf("DeletedParameters = %v, want none for a malformed recorded name", provider.DeletedParameters)
	}
}

func TestDestroyCobraDryRunDoesNotDeleteAdminPassword(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, adminPassStateJSON(testAdminParam))

	provider := &testutil.TestProvider{}
	if _, err := runDestroy(t, testutil.NewTestRuntime(provider), "--dry-run"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(provider.DeletedParameters) != 0 {
		t.Errorf("dry run deleted %v, want none", provider.DeletedParameters)
	}
}

// readPerforceModule reads the local state under dir and returns the perforce module.
func readPerforceModule(t *testing.T, dir string) *fabricastate.ModuleState {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".fabrica", "state.json"))
	if err != nil {
		t.Fatalf("reading state file: %v", err)
	}
	var st fabricastate.State
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("parsing state file: %v", err)
	}
	return st.GetModule("perforce")
}
