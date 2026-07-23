package rollback_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/deploy/rollback"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, opts := testutil.BuildTestRoot(out)
	optionsSource := func() globals.Options { return *opts }
	root.AddCommand(rollback.New(runtimeSource, optionsSource, out))
	return root
}

func runRollback(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"rollback"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

// deployStateWithFleets returns a JSON string with deploy module having an alias and two fleets.
// activeFleet has role="active", supersededFleet has role="superseded".
func deployStateWithFleets(activeFleet, supersededFleet string) string {
	resources := `[
		{"typeName":"AWS::GameLift::Alias","identifier":"alias-1"},
		{"typeName":"AWS::GameLift::Fleet","identifier":"` + activeFleet + `","properties":{"role":"active","buildVersion":"v2"}}`
	if supersededFleet != "" {
		resources += `,
		{"typeName":"AWS::GameLift::Fleet","identifier":"` + supersededFleet + `","properties":{"role":"superseded","buildVersion":"v1"}}`
	}
	resources += `]`
	return `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"deploy","version":"v2","status":"ready","resources":` + resources + `}]}`
}

// TestRollbackCobraHappyPath verifies the happy path: two fleets exist,
// rollback flips the alias and outputs confirmation + next steps.
func TestRollbackCobraHappyPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, deployStateWithFleets("fleet-new", "fleet-old"))

	provider := &cobraFakeProvider{
		fleetStatusMap: map[string]string{"fleet-old": "ACTIVE"},
	}
	got, err := runRollback(t, testutil.NewTestRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "fleet-old")
	testutil.AssertContains(t, got, "Rolled back")
	testutil.AssertContains(t, got, "Next steps:")
	testutil.AssertContains(t, got, "fabrica deploy status")

	// Verify state was updated: alias flip call made.
	if provider.updateCalls != 1 {
		t.Errorf("expected 1 update call (alias flip), got %d", provider.updateCalls)
	}
}

// TestRollbackCobraNoCandidate verifies error when only one fleet exists.
func TestRollbackCobraNoCandidate(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, deployStateWithFleets("fleet-new", ""))

	_, err := runRollback(t, testutil.NewTestRuntime(&cobraFakeProvider{}))
	if err == nil {
		t.Fatal("expected error when no previous fleet to roll back to")
	}
	if err.Error() != "no previous fleet to roll back to — only one fleet has been promoted. Nothing to do" {
		t.Errorf("expected 'no previous fleet' error, got: %v", err)
	}
}

// TestRollbackCobraNotProvisioned verifies error when deploy module doesn't exist.
func TestRollbackCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	_, err := runRollback(t, testutil.NewTestRuntime(&cobraFakeProvider{}))
	if err == nil {
		t.Fatal("expected error when deploy not set up")
	}
	if err.Error() != "deploy is not set up. Run 'fabrica deploy setup' first" {
		t.Errorf("expected 'not set up' error, got: %v", err)
	}
}

// TestRollbackCobraNilProviderFails verifies nil provider error.
func TestRollbackCobraNilProviderFails(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, deployStateWithFleets("fleet-new", "fleet-old"))

	src := testutil.NewNilProviderRuntime()
	_, err := runRollback(t, src)
	if err == nil {
		t.Fatal("expected error with nil provider")
	}
}

// TestRollbackCobraYesFlagBypassesConfirm verifies --yes skips the confirmation prompt.
func TestRollbackCobraYesFlagBypassesConfirm(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, deployStateWithFleets("fleet-new", "fleet-old"))

	provider := &cobraFakeProvider{
		fleetStatusMap: map[string]string{"fleet-old": "ACTIVE"},
	}
	got, err := runRollback(t, testutil.NewTestRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.updateCalls != 1 {
		t.Errorf("expected alias flip with --yes, got %d update calls", provider.updateCalls)
	}
	testutil.AssertContains(t, got, "Rolled back")
}

// TestRollbackCobraJSONDryRun verifies --json --dry-run work together.
func TestRollbackCobraJSONDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, deployStateWithFleets("fleet-new", "fleet-old"))

	_, err := runRollback(t, testutil.NewTestRuntime(&cobraFakeProvider{
		fleetStatusMap: map[string]string{"fleet-old": "ACTIVE"},
	}), "--json", "--dry-run", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestRollbackCobraRuntimeError verifies runtimeSource errors surface as command errors.
func TestRollbackCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, context.Canceled
	}
	_, err := runRollback(t, src)
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// cobraFakeProvider implements Provider + GameLiftManager for rollback Cobra-layer tests.
type cobraFakeProvider struct {
	updateCalls    int
	fleetStatusMap map[string]string // fleetID -> status
}

func (f *cobraFakeProvider) Name() string { return "fake" }

func (f *cobraFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *cobraFakeProvider) Resources() cloud.ResourceClient {
	return &cobraFakeRC{provider: f}
}

// FleetStatus implements cloud.GameLiftManager.
func (f *cobraFakeProvider) FleetStatus(_ context.Context, fleetID string) (cloud.FleetInfo, error) {
	status := "ACTIVE"
	if s, ok := f.fleetStatusMap[fleetID]; ok {
		status = s
	}
	return cloud.FleetInfo{FleetID: fleetID, Status: status}, nil
}

// FleetEvents implements cloud.GameLiftManager.
func (f *cobraFakeProvider) FleetEvents(_ context.Context, _ string) ([]cloud.FleetEvent, error) {
	return nil, nil
}

// CreateFleetAsync implements cloud.GameLiftManager.
func (f *cobraFakeProvider) CreateFleetAsync(_ context.Context, _ *cloud.Resource) error {
	return nil
}

type cobraFakeRC struct {
	provider *cobraFakeProvider
}

func (r *cobraFakeRC) Create(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *cobraFakeRC) Get(_ context.Context, _ *cloud.Resource) error    { return nil }
func (r *cobraFakeRC) Update(_ context.Context, _ *cloud.Resource) error {
	r.provider.updateCalls++
	return nil
}
func (r *cobraFakeRC) Delete(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *cobraFakeRC) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}
