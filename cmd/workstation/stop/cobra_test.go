package stop_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/cmd/workstation/stop"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, opts := testutil.BuildTestRoot(out)
	optionsSource := func() globals.Options { return *opts }
	root.AddCommand(stop.New(runtimeSource, optionsSource, out))
	return root
}

func runStop(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"stop"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func newRuntime(provider fabricac.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

func TestStopCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runStop(t, newRuntime(&cobraFakeProvider{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "not provisioned")
}

func TestStopCobraDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON("ready"))

	provider := &cobraFakeProvider{}
	got, err := runStop(t, newRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.AssertContains(t, got, "dry run")
	if provider.stopCalls != 0 {
		t.Errorf("dry-run made %d stop calls, want 0", provider.stopCalls)
	}
}

func TestStopCobraYesFlag(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON("ready"))

	provider := &cobraFakeProvider{}
	got, err := runStop(t, newRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.stopCalls != 1 {
		t.Errorf("expected 1 stop call, got %d", provider.stopCalls)
	}
	testutil.AssertContains(t, got, "stopped")
}

func TestStopCobraAlreadyStopped(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON("stopped"))

	provider := &cobraFakeProvider{}
	got, err := runStop(t, newRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.stopCalls != 0 {
		t.Errorf("already-stopped: expected 0 stop calls, got %d", provider.stopCalls)
	}
	testutil.AssertContains(t, got, "already stopped")
}

func TestStopCobraJSONYes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	testutil.WriteStateFile(t, dir, provisionedStateJSON("ready"))

	got, err := runStop(t, newRuntime(&cobraFakeProvider{}), "--json", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result stop.StopOutput
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if result.DryRun {
		t.Error("dryRun must be false")
	}
	if result.Status != "stopped" {
		t.Errorf("status = %q, want stopped", result.Status)
	}
}

func TestStopCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	_, err := runStop(t, src)
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// ---- helpers ----

func provisionedStateJSON(status string) string {
	return `{"account":"123456789012","region":"us-east-1","modules":[
		{"name":"workstation","version":"ami-test","status":"` + status + `","resources":[
			{"typeName":"AWS::EC2::SecurityGroup","identifier":"sg-cobrawstest"},
			{"typeName":"AWS::EC2::Instance","identifier":"i-cobrawstest"}
		]}]}`
}

// cobraFakeProvider implements both cloud.Provider and cloud.EC2InstanceManager.
type cobraFakeProvider struct {
	stopCalls  int
	startCalls int
	stopErr    error
}

func (f *cobraFakeProvider) Name() string { return "fake" }
func (f *cobraFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}
func (f *cobraFakeProvider) Resources() fabricac.ResourceClient {
	return &cobraFakeRC{}
}
func (f *cobraFakeProvider) StopInstance(_ context.Context, _ string) error {
	f.stopCalls++
	return f.stopErr
}
func (f *cobraFakeProvider) StartInstance(_ context.Context, _ string) error {
	f.startCalls++
	return nil
}

type cobraFakeRC struct{}

func (r *cobraFakeRC) Create(_ context.Context, _ *fabricac.Resource) error { return nil }
func (r *cobraFakeRC) Get(_ context.Context, _ *fabricac.Resource) error    { return nil }
func (r *cobraFakeRC) Update(_ context.Context, _ *fabricac.Resource) error { return nil }
func (r *cobraFakeRC) Delete(_ context.Context, _ *fabricac.Resource) error { return nil }
func (r *cobraFakeRC) List(_ context.Context, _ string) ([]fabricac.Resource, error) {
	return nil, nil
}
