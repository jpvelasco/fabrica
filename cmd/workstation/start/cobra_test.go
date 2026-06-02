package start_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/workstation/start"
	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
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
	root.AddCommand(start.New(runtimeSource, optionsSource, out))
	return root
}

func runStart(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"start"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func newRuntime(provider fabricac.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

func TestStartCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := runStart(t, newRuntime(&cobraFakeProvider{}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCobraContains(t, got, "not provisioned")
}

func TestStartCobraDryRun(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, provisionedStateJSON("stopped"))

	provider := &cobraFakeProvider{}
	got, err := runStart(t, newRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertCobraContains(t, got, "dry run")
	if provider.startCalls != 0 {
		t.Errorf("dry-run made %d start calls, want 0", provider.startCalls)
	}
}

func TestStartCobraYesFlag(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, provisionedStateJSON("stopped"))

	provider := &cobraFakeProvider{}
	got, err := runStart(t, newRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.startCalls != 1 {
		t.Errorf("expected 1 start call, got %d", provider.startCalls)
	}
	assertCobraContains(t, got, "started")
}

func TestStartCobraAlreadyRunning(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, provisionedStateJSON("ready"))

	provider := &cobraFakeProvider{}
	got, err := runStart(t, newRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if provider.startCalls != 0 {
		t.Errorf("already-running: expected 0 start calls, got %d", provider.startCalls)
	}
	assertCobraContains(t, got, "already running")
}

func TestStartCobraJSONYes(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeStateFile(t, dir, provisionedStateJSON("stopped"))

	got, err := runStart(t, newRuntime(&cobraFakeProvider{}), "--json", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result start.StartOutput
	if err := json.Unmarshal([]byte(got), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, got)
	}
	if result.DryRun {
		t.Error("dryRun must be false")
	}
	if result.Status != "ready" {
		t.Errorf("status = %q, want ready", result.Status)
	}
}

func TestStartCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	_, err := runStart(t, src)
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

// cobraFakeProvider implements both cloud.Provider and cloud.EC2InstanceManager.
type cobraFakeProvider struct {
	startCalls int
	startErr   error
}

func (f *cobraFakeProvider) Name() string { return "fake" }
func (f *cobraFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}
func (f *cobraFakeProvider) Resources() fabricac.ResourceClient {
	return &cobraFakeRC{}
}
func (f *cobraFakeProvider) StopInstance(_ context.Context, _ string) error { return nil }
func (f *cobraFakeProvider) StartInstance(_ context.Context, _ string) error {
	f.startCalls++
	return f.startErr
}

type cobraFakeRC struct{}

func (r *cobraFakeRC) Create(_ context.Context, _ *fabricac.Resource) error { return nil }
func (r *cobraFakeRC) Get(_ context.Context, _ *fabricac.Resource) error    { return nil }
func (r *cobraFakeRC) Update(_ context.Context, _ *fabricac.Resource) error { return nil }
func (r *cobraFakeRC) Delete(_ context.Context, _ *fabricac.Resource) error { return nil }
func (r *cobraFakeRC) List(_ context.Context, _ string) ([]fabricac.Resource, error) {
	return nil, nil
}
