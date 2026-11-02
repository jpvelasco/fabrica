package terminate_test

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
	"github.com/jpvelasco/fabrica/cmd/workstation/terminate"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/stateutil"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(terminate.New(runtimeSource, optionsSource, out))
	return root
}

func runTerminate(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"terminate"}, args...)...)
}

// TestTerminateCobraNotProvisioned verifies clean message when no state on disk.
func TestTerminateCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runTerminate(t, testutil.NewTestRuntime(&testutil.TestProvider{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
}

// TestTerminateCobraDryRunNoDeleteCalls verifies --dry-run produces output without calling delete.
func TestTerminateCobraDryRunNoDeleteCalls(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())

	provider := &testutil.TestProvider{}
	got, err := runTerminate(t, testutil.NewTestRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "dry run")
	testutil.AssertContains(t, got, testWSParamName)
	if provider.DeleteCalls != 0 {
		t.Errorf("dry-run made %d delete calls, want 0", provider.DeleteCalls)
	}
	if len(provider.DeletedParameters) != 0 {
		t.Errorf("dry-run deleted SSM parameters: %v", provider.DeletedParameters)
	}
}

// TestTerminateCobraYesFlagTerminatesResources verifies --yes terminates without prompt.
func TestTerminateCobraYesFlagTerminatesResources(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())

	provider := &testutil.TestProvider{}
	got, err := runTerminate(t, testutil.NewTestRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.DeleteCalls != 2 {
		t.Errorf("expected 2 delete calls, got %d", provider.DeleteCalls)
	}
	// terminate deletes exactly the per-create name recorded on the
	// instance, never a shared name.
	if len(provider.DeletedParameters) != 1 || provider.DeletedParameters[0] != testWSParamName {
		t.Errorf("deleted parameters = %v, want %q once", provider.DeletedParameters, testWSParamName)
	}
	testutil.AssertContains(t, got, "terminated")
}

// TestTerminateCobraJSONYes verifies --json --yes output after successful terminate.
func TestTerminateCobraJSONYes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())

	got, err := runTerminate(t, testutil.NewTestRuntime(&testutil.TestProvider{}), "--json", "--yes")
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
	if len(result.Destroyed) != 3 {
		t.Errorf("expected 3 destroyed (instance, security group, session parameter), got %d: %v", len(result.Destroyed), result.Destroyed)
	}
	found := false
	for _, id := range result.Destroyed {
		if id == testWSParamName {
			found = true
		}
	}
	if !found {
		t.Errorf("destroyed = %v, missing %s", result.Destroyed, testWSParamName)
	}
}

// TestTerminateCobraJSONNotProvisioned verifies --json output when not provisioned.
func TestTerminateCobraJSONNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runTerminate(t, testutil.NewTestRuntime(&testutil.TestProvider{}), "--json")
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

// TestTerminateCobraRuntimeError verifies runtimeSource errors surface as command errors.
func TestTerminateCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	_, err := runTerminate(t, src)
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// TestNewTeardownWiring verifies NewTeardown returns a Command with correct wiring.
func TestNewTeardownWiring(t *testing.T) {
	var out bytes.Buffer
	rt := globals.Runtime{Config: config.Defaults(), Provider: &testutil.TestProvider{}}
	tc := terminate.NewTeardown(rt, &out)
	if !tc.SkipConfirm || !tc.AssumeYes {
		t.Fatalf("SkipConfirm/AssumeYes must be true; got SkipConfirm=%v, AssumeYes=%v", tc.SkipConfirm, tc.AssumeYes)
	}
	if tc.ReadState == nil || tc.WriteState == nil || tc.DeleteResource == nil || tc.GetResource == nil {
		t.Fatal("provider seams must be wired when provider is non-nil")
	}
	if tc.Runtime.Provider == nil {
		t.Fatal("Runtime.Provider must be set")
	}
	if tc.Spec.ModuleName != "workstation" {
		t.Errorf("module name = %q, want workstation", tc.Spec.ModuleName)
	}
}

// TestNewTeardownNilProvider verifies NewTeardown handles nil provider gracefully.
func TestNewTeardownNilProvider(t *testing.T) {
	var out bytes.Buffer
	rt := globals.Runtime{Config: config.Defaults(), Provider: nil}
	tc := terminate.NewTeardown(rt, &out)
	if !tc.SkipConfirm || !tc.AssumeYes {
		t.Fatal("SkipConfirm/AssumeYes must be true even with nil provider")
	}
	if tc.ReadState == nil || tc.WriteState == nil {
		t.Fatal("ReadState and WriteState must always be wired")
	}
	if tc.DeleteResource != nil || tc.GetResource != nil {
		t.Fatal("DeleteResource/GetResource must be nil when provider is nil")
	}
	if tc.SDKDeleteFunc != nil {
		t.Fatal("SDKDeleteFunc must be nil when provider is nil")
	}
}

// TestTerminateCobraParameterDeleteFailureKeepsState pins the #524 invariant:
// a failed session-password parameter delete aborts terminate before the EC2
// instance is deleted, so state keeps the instance record — and with it the
// parameter name — and a retry can still find and delete the parameter.
func TestTerminateCobraParameterDeleteFailureKeepsState(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())

	failing := &testutil.TestProvider{DeleteParameterErr: errors.New("ssm unavailable")}
	_, err := runTerminate(t, testutil.NewTestRuntime(failing), "--yes")
	if err == nil {
		t.Fatal("expected terminate to fail when the parameter delete fails")
	}
	if !strings.Contains(err.Error(), testWSParamName) {
		t.Errorf("error = %v, want it to name the failing parameter", err)
	}
	if failing.DeleteCalls != 0 {
		t.Errorf("resource deletes = %d, want 0 (the failed parameter delete must abort before the instance)", failing.DeleteCalls)
	}
	if len(failing.DeletedParameters) != 0 {
		t.Errorf("DeletedParameters = %v, want none (the delete failed)", failing.DeletedParameters)
	}

	m := readWorkstationState(t, dir)
	if m == nil {
		t.Fatal("workstation module missing from state after failed terminate; a retry would orphan the parameter")
	}
	instRes, ok := stateutil.ResourceByType(m, cloud.TypeAWSEC2Instance)
	if !ok {
		t.Fatal("instance record missing from state; its sessionPasswordParameter property is the only name reference")
	}
	if got := instRes.Properties["sessionPasswordParameter"]; got != testWSParamName {
		t.Errorf("recorded parameter name = %q, want %q", got, testWSParamName)
	}

	// Retry with a fresh provider: the parameter delete now succeeds (a
	// missing parameter is a no-op), the rest of the inventory is deleted,
	// and the module is removed.
	healthy := &testutil.TestProvider{}
	if _, err := runTerminate(t, testutil.NewTestRuntime(healthy), "--yes"); err != nil {
		t.Fatalf("retry after a failed parameter delete: %v", err)
	}
	if len(healthy.DeletedParameters) != 1 || healthy.DeletedParameters[0] != testWSParamName {
		t.Errorf("retry DeletedParameters = %v, want %q", healthy.DeletedParameters, testWSParamName)
	}
	if healthy.DeleteCalls != 2 {
		t.Errorf("retry resource deletes = %d, want 2 (instance + SG)", healthy.DeleteCalls)
	}
	if m := readWorkstationState(t, dir); m != nil {
		t.Errorf("workstation module still in state after successful retry: %+v", m)
	}
}

// TestTerminateCobraInterruptionBetweenDeletesCoversParameter pins the
// mid-run failure window: when the instance delete fails after the parameter
// delete already succeeded, the instance record (and the parameter name on
// it) stays in state, so a retry still sees the full inventory. Because the
// parameter is deleted before the instance, its name is only dropped from
// state after it is already gone from AWS — no failure window can orphan it.
func TestTerminateCobraInterruptionBetweenDeletesCoversParameter(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())

	failing := &testutil.TestProvider{
		DeleteErr: map[string]error{cloud.TypeAWSEC2Instance: errors.New("termination failed")},
	}
	_, err := runTerminate(t, testutil.NewTestRuntime(failing), "--yes")
	if err == nil {
		t.Fatal("expected terminate to fail when the instance delete fails")
	}
	// The parameter is deleted before the instance, so it is gone from AWS
	// while its name is still recorded on the instance record in state.
	if len(failing.DeletedParameters) != 1 || failing.DeletedParameters[0] != testWSParamName {
		t.Errorf("DeletedParameters = %v, want %q deleted before the failed instance delete", failing.DeletedParameters, testWSParamName)
	}
	if failing.DeleteCalls != 1 {
		t.Errorf("resource deletes = %d, want 1 (the instance; the failure stops before the SG)", failing.DeleteCalls)
	}

	m := readWorkstationState(t, dir)
	if m == nil {
		t.Fatal("workstation module missing from state after interrupted terminate")
	}
	instRes, ok := stateutil.ResourceByType(m, cloud.TypeAWSEC2Instance)
	if !ok {
		t.Fatal("instance record missing from state; its sessionPasswordParameter property is the only name reference")
	}
	if got := instRes.Properties["sessionPasswordParameter"]; got != testWSParamName {
		t.Errorf("recorded parameter name = %q, want %q", got, testWSParamName)
	}

	healthy := &testutil.TestProvider{}
	if _, err := runTerminate(t, testutil.NewTestRuntime(healthy), "--yes"); err != nil {
		t.Fatalf("retry after an interrupted terminate: %v", err)
	}
	if len(healthy.DeletedParameters) != 1 || healthy.DeletedParameters[0] != testWSParamName {
		t.Errorf("retry DeletedParameters = %v, want %q", healthy.DeletedParameters, testWSParamName)
	}
	if healthy.DeleteCalls != 2 {
		t.Errorf("retry resource deletes = %d, want 2 (instance + SG)", healthy.DeleteCalls)
	}
	if m := readWorkstationState(t, dir); m != nil {
		t.Errorf("workstation module still in state after successful retry: %+v", m)
	}
}

// TestTerminateCobraInstanceAlreadyTerminated verifies the parameter is
// deleted even when the instance is already gone from AWS: the pre-delete
// check skips the instance, but the parameter (first in the delete order) is
// removed first, so the SecureString is not left behind.
func TestTerminateCobraInstanceAlreadyTerminated(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON())

	provider := &testutil.TestProvider{
		GetResources: map[string]cloud.Resource{
			cloud.TypeAWSEC2Instance: {
				TypeName:    cloud.TypeAWSEC2Instance,
				Identifier:  "i-cobrawstest",
				ActualState: []byte(`{"State":{"Name":"terminated"}}`),
			},
		},
	}
	got, err := runTerminate(t, testutil.NewTestRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "Already deleted")
	if len(provider.DeletedParameters) != 1 || provider.DeletedParameters[0] != testWSParamName {
		t.Errorf("DeletedParameters = %v, want %q", provider.DeletedParameters, testWSParamName)
	}
	if provider.DeleteCalls != 1 {
		t.Errorf("resource deletes = %d, want 1 (the SG only; the terminated instance is skipped)", provider.DeleteCalls)
	}
	if m := readWorkstationState(t, dir); m != nil {
		t.Errorf("workstation module still in state after terminate: %+v", m)
	}
}

// ---- helpers ----

// testWSParamName is the per-create session-password parameter name the
// fixture records on the instance (Properties "sessionPasswordParameter").
const testWSParamName = "/fabrica/workstation/abc123def4567890abc123def4567890/dcv-password"

func provisionedStateJSON() string {
	return testutil.NewProvisionedStateJSON(testutil.StateModule{
		Name: "workstation", Version: "ami-test", Status: "ready",
		Resources: []testutil.StateResource{
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-cobrawstest"},
			{TypeName: "AWS::EC2::Instance", Identifier: "i-cobrawstest", Properties: map[string]any{"sessionPasswordParameter": testWSParamName}},
		},
	})
}

// readWorkstationState reads the local state file under dir and returns the
// workstation module, or nil when the module is absent.
func readWorkstationState(t *testing.T, dir string) *fabricastate.ModuleState {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, ".fabrica", "state.json"))
	if err != nil {
		t.Fatalf("reading state file: %v", err)
	}
	var st fabricastate.State
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("parsing state file: %v", err)
	}
	return st.GetModule("workstation")
}
