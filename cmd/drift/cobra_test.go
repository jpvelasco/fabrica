package driftcmd_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	driftcmd "github.com/jpvelasco/fabrica/cmd/drift"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(driftcmd.New(runtimeSource, optionsSource, out))
	return root
}

func runDrift(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	return testutil.RunCommandWithOut(t, root, &out, append([]string{"drift"}, args...)...)
}

// TestDriftCobraEmpty verifies a clean exit when no state exists.
func TestDriftCobraEmpty(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runDrift(t, testutil.NewNilProviderRuntime())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "No modules provisioned") {
		t.Errorf("expected 'No modules provisioned'; got:\n%s", got)
	}
}

// TestDriftCobraJSON verifies --json produces a parseable DriftReport.
func TestDriftCobraJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runDrift(t, testutil.NewNilProviderRuntime(), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(got), &report); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if _, ok := report["backend"]; !ok {
		t.Error("expected 'backend' field in JSON output")
	}
	if _, ok := report["modules"]; !ok {
		t.Error("expected 'modules' field in JSON output")
	}
}

// TestDriftCobraRuntimeError verifies runtimeSource errors surface as command errors.
func TestDriftCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	if _, err := runDrift(t, src); err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// TestDriftCobraWithModules verifies drift runs with provisioned modules and
// nil provider (all resources show as error since no resource client).
func TestDriftCobraWithModules(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	testutil.WriteStateFile(t, dir, testutil.NewProvisionedStateJSON(
		testutil.StateModule{
			Name:      "horde",
			Version:   "ami-fake",
			Status:    "ready",
			Resources: testutil.EC2Pair("sg-123", "i-123"),
		},
	))

	got, err := runDrift(t, testutil.NewNilProviderRuntime())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "horde") {
		t.Errorf("expected 'horde' in output; got:\n%s", got)
	}
	if !strings.Contains(got, "Summary") {
		t.Errorf("expected 'Summary' in output; got:\n%s", got)
	}
}

// TestDriftCobraWithModulesJSON verifies --json with provisioned modules.
func TestDriftCobraWithModulesJSON(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	testutil.WriteStateFile(t, dir, testutil.NewProvisionedStateJSON(
		testutil.StateModule{
			Name:      "horde",
			Version:   "ami-fake",
			Status:    "ready",
			Resources: testutil.EC2Pair("sg-123", "i-123"),
		},
	))

	got, err := runDrift(t, testutil.NewNilProviderRuntime(), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(got), &report); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	modules, ok := report["modules"].([]any)
	if !ok || len(modules) != 1 {
		t.Fatalf("expected 1 module in JSON, got %d", len(modules))
	}
}

// TestDriftCobraMissingResource verifies missing resources show as [FAIL]
// and the summary counts them.
func TestDriftCobraMissingResource(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	testutil.WriteStateFile(t, dir, testutil.NewProvisionedStateJSON(
		testutil.StateModule{
			Name:      "horde",
			Version:   "ami-fake",
			Status:    "ready",
			Resources: testutil.EC2Pair("sg-123", "i-123"),
		},
	))

	provider := &testutil.TestProvider{
		GetResources: map[string]cloud.Resource{
			cloud.TypeAWSEC2SecurityGroup: {Identifier: "sg-123"},
		},
	}

	got, err := runDrift(t, func() (globals.Runtime, error) {
		return globals.Runtime{
			Config:   config.Defaults(),
			Provider: &missingInstanceProvider{TestProvider: provider},
		}, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "[FAIL]") {
		t.Errorf("expected [FAIL] for missing resource; got:\n%s", got)
	}
	if !strings.Contains(got, "Missing") {
		t.Errorf("expected 'Missing' in summary; got:\n%s", got)
	}
}

// missingInstanceProvider returns ErrResourceNotFound for EC2 instances.
type missingInstanceProvider struct {
	*testutil.TestProvider
}

func (m *missingInstanceProvider) Resources() cloud.ResourceClient {
	return &missingInstanceClient{testProvider: m.TestProvider}
}

type missingInstanceClient struct {
	testProvider *testutil.TestProvider
}

func (c *missingInstanceClient) Create(_ context.Context, res *cloud.Resource) error {
	if c.testProvider.CreateErr != nil {
		if err, ok := c.testProvider.CreateErr[res.TypeName]; ok {
			return err
		}
	}
	return nil
}

func (c *missingInstanceClient) Get(_ context.Context, res *cloud.Resource) error {
	if res.TypeName == cloud.TypeAWSEC2Instance {
		return cloud.ErrResourceNotFound
	}
	if c.testProvider.GetResources != nil {
		if stored, ok := c.testProvider.GetResources[res.TypeName]; ok {
			res.Identifier = stored.Identifier
			res.ActualState = stored.ActualState
		}
	}
	return nil
}

func (c *missingInstanceClient) Update(_ context.Context, _ *cloud.Resource) error {
	return nil
}

func (c *missingInstanceClient) Delete(_ context.Context, _ *cloud.Resource) error {
	return nil
}

func (c *missingInstanceClient) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}

// TestDriftCobraStoppedInstance verifies a stopped EC2 instance shows as
// Mismatch with [WARN] and the summary counts it.
func TestDriftCobraStoppedInstance(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	testutil.WriteStateFile(t, dir, testutil.NewProvisionedStateJSON(
		testutil.StateModule{
			Name:      "horde",
			Version:   "ami-fake",
			Status:    "ready",
			Resources: testutil.EC2Pair("sg-123", "i-123"),
		},
	))

	stoppedState := []byte(`{"State":{"Name":"stopped"},"InstanceType":"m7i.2xlarge","ImageId":"ami-fake"}`)
	provider := &testutil.TestProvider{
		GetResources: map[string]cloud.Resource{
			cloud.TypeAWSEC2Instance:      {Identifier: "i-123", ActualState: stoppedState},
			cloud.TypeAWSEC2SecurityGroup: {Identifier: "sg-123"},
		},
	}

	got, err := runDrift(t, testutil.NewTestRuntime(provider))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "[WARN]") {
		t.Errorf("expected [WARN] for stopped instance; got:\n%s", got)
	}
	if !strings.Contains(got, "Mismatch") {
		t.Errorf("expected 'Mismatch' in summary; got:\n%s", got)
	}
}

// TestDriftCobraBackendChecker verifies backend checker with bucket/table
// config shows bucket and table status with details.
func TestDriftCobraBackendChecker(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	testutil.WriteStateFile(t, dir, testutil.NewProvisionedStateJSON())

	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-123456789012"
	cfg.State.Table = "fabrica-state-lock"

	provider := &testutil.TestProvider{}
	rt := globals.Runtime{
		Config:   cfg,
		Provider: &backendCheckerProvider{TestProvider: provider, bucketExists: true, tableExists: false},
	}

	got, err := runDrift(t, func() (globals.Runtime, error) { return rt, nil })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "fabrica-state-123456789012") {
		t.Errorf("expected bucket name in output; got:\n%s", got)
	}
	if !strings.Contains(got, "lock table not found") {
		t.Errorf("expected 'lock table not found' detail; got:\n%s", got)
	}
}

// backendCheckerProvider implements StateBackendChecker for testing.
type backendCheckerProvider struct {
	*testutil.TestProvider
	bucketExists bool
	tableExists  bool
	bucketErr    error
	tableErr     error
}

func (b *backendCheckerProvider) StateBucketExists(_ context.Context, _ string) (bool, error) {
	return b.bucketExists, b.bucketErr
}

func (b *backendCheckerProvider) StateLockTableExists(_ context.Context, _ string) (bool, error) {
	return b.tableExists, b.tableErr
}

// TestDriftCobraCodeBuildMissing verifies CodeBuild project missing via
// CodeBuildRunner auxiliary interface.
func TestDriftCobraCodeBuildMissing(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	testutil.WriteStateFile(t, dir, testutil.NewProvisionedStateJSON(
		testutil.StateModule{
			Name:    "ci",
			Status:  "ready",
			Version: "v1",
			Resources: []testutil.StateResource{
				{TypeName: "AWS::IAM::Role", Identifier: "role-ci"},
				{TypeName: "AWS::CodeBuild::Project", Identifier: "horde-build"},
			},
		},
	))

	provider := &testutil.CodeBuildProvider{
		ProjectExistsResult: false,
	}

	got, err := runDrift(t, testutil.NewTestRuntime(provider), "--json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(got), &report); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if v, ok := report["missing"].(float64); !ok || v != 1 {
		t.Errorf("expected 1 missing in JSON report, got: %v", report["missing"])
	}
}
