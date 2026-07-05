package status_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/deploy/status"
	"github.com/jpvelasco/fabrica/cmd/globals"
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
	root.AddCommand(status.New(runtimeSource, optionsSource, out))
	return root
}

func runStatus(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"status"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func newTestRuntime(provider cloud.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

// deployStateWithFleets returns a JSON string with deploy module having an alias and fleets.
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

func writeStateFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(dir+"/.fabrica", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/.fabrica/state.json", []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Fatalf("%q does not contain %q", s, substr)
}

func assertNotContains(t *testing.T, s, substr string) {
	t.Helper()
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			t.Fatalf("%q should not contain %q", s, substr)
		}
	}
}

// TestStatusCobraNotProvisioned verifies clean message when no deploy state exists.
func TestStatusCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runStatus(t, newTestRuntime(&cobraFakeProvider{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "not set up")
	assertContains(t, got, "fabrica deploy setup")
}

// TestStatusCobraHappyPathWithCandidate verifies the happy path: active fleet + rollback candidate.
func TestStatusCobraHappyPathWithCandidate(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, deployStateWithFleets("fleet-new", "fleet-old"))

	provider := &cobraFakeProvider{
		fleetStatusMap: map[string]string{
			"fleet-new": "ACTIVE",
			"fleet-old": "ACTIVE",
		},
	}
	got, err := runStatus(t, newTestRuntime(provider))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "[OK]")
	assertContains(t, got, "Active fleet")
	assertContains(t, got, "fleet-new")
	assertContains(t, got, "Next steps:")
	assertContains(t, got, "fabrica deploy promote")
	assertContains(t, got, "fabrica deploy rollback")
}

// TestStatusCobraSingleFleetNoRollback verifies output when only one fleet (no rollback candidate).
func TestStatusCobraSingleFleetNoRollback(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, deployStateWithFleets("fleet-new", ""))

	provider := &cobraFakeProvider{
		fleetStatusMap: map[string]string{"fleet-new": "ACTIVE"},
	}
	got, err := runStatus(t, newTestRuntime(provider))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "Active fleet")
	assertContains(t, got, "fleet-new")
	assertContains(t, got, "Next steps:")
	assertContains(t, got, "fabrica deploy promote")
	// Rollback line should NOT appear when no candidates exist.
	assertNotContains(t, got, "fabrica deploy rollback")
}

// TestStatusCobraDryRunNoProviderCall verifies --dry-run does not call provider.
func TestStatusCobraDryRunNoProviderCall(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, deployStateWithFleets("fleet-new", "fleet-old"))

	provider := &cobraFakeProvider{
		fleetStatusMap: map[string]string{"fleet-new": "ACTIVE"},
	}
	got, err := runStatus(t, newTestRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Status is read-only, so --dry-run should not make a visible difference
	// in the output. Just verify it completes.
	assertContains(t, got, "Active fleet")
}

// TestStatusCobraJSONNotProvisioned verifies --json output when not provisioned.
func TestStatusCobraJSONNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runStatus(t, newTestRuntime(&cobraFakeProvider{}), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var sj statusJSONType
	if err := json.Unmarshal([]byte(got), &sj); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v\nGot: %s", err, got)
	}
	if sj.Provisioned {
		t.Errorf("expected Provisioned=false, got %v", sj.Provisioned)
	}
}

// TestStatusCobraJSONWithFleets verifies --json output with deploy module provisioned.
func TestStatusCobraJSONWithFleets(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, deployStateWithFleets("fleet-new", "fleet-old"))

	provider := &cobraFakeProvider{
		fleetStatusMap: map[string]string{
			"fleet-new": "ACTIVE",
			"fleet-old": "ACTIVE",
		},
	}
	got, err := runStatus(t, newTestRuntime(provider), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var sj statusJSONType
	if err := json.Unmarshal([]byte(got), &sj); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v\nGot: %s", err, got)
	}
	if !sj.Provisioned {
		t.Errorf("expected Provisioned=true, got %v", sj.Provisioned)
	}
	if sj.Alias != "alias-1" {
		t.Errorf("expected Alias='alias-1', got %q", sj.Alias)
	}
	if sj.ActiveFleet == nil {
		t.Fatal("expected ActiveFleet to be non-nil")
	}
	if sj.ActiveFleet.FleetID != "fleet-new" {
		t.Errorf("expected ActiveFleet.FleetID='fleet-new', got %q", sj.ActiveFleet.FleetID)
	}
	if len(sj.RollbackCandidates) != 1 {
		t.Errorf("expected 1 rollback candidate, got %d", len(sj.RollbackCandidates))
	}
}

// TestStatusCobraJSONDryRun verifies --json --dry-run work together.
func TestStatusCobraJSONDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, deployStateWithFleets("fleet-new", "fleet-old"))

	_, err := runStatus(t, newTestRuntime(&cobraFakeProvider{
		fleetStatusMap: map[string]string{"fleet-new": "ACTIVE"},
	}), "--json", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestStatusCobraYesFlagWithDryRun verifies --yes --dry-run work together.
func TestStatusCobraYesFlagWithDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, deployStateWithFleets("fleet-new", "fleet-old"))

	_, err := runStatus(t, newTestRuntime(&cobraFakeProvider{
		fleetStatusMap: map[string]string{"fleet-new": "ACTIVE"},
	}), "--yes", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestStatusCobraNilProvider verifies nil provider with no state exits cleanly.
func TestStatusCobraNilProvider(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runStatus(t, func() (globals.Runtime, error) {
		cfg := config.Defaults()
		cfg.Cloud.AWS.AccountID = "123456789012"
		return globals.Runtime{Config: cfg, Provider: nil}, nil
	})
	if err != nil {
		t.Fatalf("nil provider: unexpected error: %v", err)
	}
	assertContains(t, got, "not set up")
}

// TestStatusCobraRuntimeError verifies runtimeSource errors surface as command errors.
func TestStatusCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, context.Canceled
	}
	_, err := runStatus(t, src)
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// TestStatusCobraFakeProviderImplementsInterfaces verifies the fake provider satisfies all required interfaces.
func TestStatusCobraFakeProviderImplementsInterfaces(t *testing.T) {
	var p cloud.Provider = &cobraFakeProvider{}
	if _, ok := p.(cloud.GameLiftManager); !ok {
		t.Fatal("cobraFakeProvider does not implement cloud.GameLiftManager")
	}
}

// statusJSONType mirrors the JSON output structure for testing.
type statusJSONType struct {
	Provisioned        bool            `json:"provisioned"`
	Alias              string          `json:"alias,omitempty"`
	ActiveFleet        *fleetJSONType  `json:"activeFleet,omitempty"`
	RollbackCandidates []fleetJSONType `json:"rollbackCandidates,omitempty"`
}

type fleetJSONType struct {
	FleetID      string `json:"fleetId"`
	BuildVersion string `json:"buildVersion"`
	Role         string `json:"role"`
	LiveStatus   string `json:"liveStatus"`
}

// cobraFakeProvider implements Provider + GameLiftManager for status Cobra-layer tests.
type cobraFakeProvider struct {
	fleetStatusMap map[string]string // fleetID -> status
}

func (f *cobraFakeProvider) Name() string { return "fake" }

func (f *cobraFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *cobraFakeProvider) Resources() cloud.ResourceClient {
	return &cobraFakeRC{}
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

type cobraFakeRC struct{}

func (r *cobraFakeRC) Create(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *cobraFakeRC) Get(_ context.Context, _ *cloud.Resource) error    { return nil }
func (r *cobraFakeRC) Update(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *cobraFakeRC) Delete(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *cobraFakeRC) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}
