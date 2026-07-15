package setup_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ci/setup"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

// buildTestRoot constructs a minimal root that mirrors production persistent flags.
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
	root.AddCommand(setup.New(runtimeSource, optionsSource, out))
	return root
}

func runCISetup(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"setup"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func newTestRuntime(provider cloud.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Fatalf("%q does not contain %q", s, substr)
	}
}

// TestSetupCobraDryRunShowsPlan exercises the real Cobra entry with --dry-run.
func TestSetupCobraDryRunShowsPlan(t *testing.T) {
	t.Chdir(t.TempDir())
	provider := &ciSetupFakeProvider{}
	got, err := runCISetup(t, newTestRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "CI setup (dry run)")
	assertContains(t, got, "IAM role")
	assertContains(t, got, "CodeBuild")
	assertContains(t, got, "Cost estimate")
	if provider.createCalls != 0 || provider.ensureCalls != 0 {
		t.Errorf("dry-run must not create resources: create=%d ensure=%d", provider.createCalls, provider.ensureCalls)
	}
}

// TestSetupCobraYesCreatesResources runs the full New→ExecuteContext path with --yes.
func TestSetupCobraYesCreatesResources(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	provider := &ciSetupFakeProvider{}
	got, err := runCISetup(t, newTestRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "CI setup complete")
	assertContains(t, got, "Next steps:")
	assertContains(t, got, "fabrica ci trigger")
	if provider.createCalls != 1 {
		t.Errorf("expected 1 IAM role create, got %d", provider.createCalls)
	}
	if provider.ensureCalls != 1 {
		t.Errorf("expected 1 EnsureProject call, got %d", provider.ensureCalls)
	}
	// State file written via the real fabricastate.WriteState path.
	if _, err := os.Stat(dir + "/.fabrica/state.json"); err != nil {
		t.Errorf("expected state file after setup: %v", err)
	}
}

// TestSetupCobraNilProvider errors through the real Cobra wiring.
func TestSetupCobraNilProvider(t *testing.T) {
	t.Chdir(t.TempDir())
	src := func() (globals.Runtime, error) {
		cfg := config.Defaults()
		return globals.Runtime{Config: cfg, Provider: nil}, nil
	}
	_, err := runCISetup(t, src, "--yes")
	if err == nil {
		t.Fatal("expected error when provider is nil")
	}
	assertContains(t, err.Error(), "no cloud provider")
}

// TestSetupCobraRuntimeError surfaces runtimeSource failures.
func TestSetupCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	_, err := runCISetup(t, src)
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// TestSetupCobraWithoutCodeBuildRunnerFails when provider lacks CodeBuildRunner.
func TestSetupCobraWithoutCodeBuildRunnerFails(t *testing.T) {
	t.Chdir(t.TempDir())
	// identityOnlyProvider implements Provider but not CodeBuildRunner.
	src := newTestRuntime(&identityOnlyProvider{})
	_, err := runCISetup(t, src, "--yes")
	if err == nil {
		t.Fatal("expected error when CodeBuildRunner is missing")
	}
	assertContains(t, err.Error(), "CodeBuild")
}

// ---- fakes ----

type ciSetupFakeProvider struct {
	createCalls int
	ensureCalls int
}

func (f *ciSetupFakeProvider) Name() string { return "fake" }

func (f *ciSetupFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *ciSetupFakeProvider) Resources() cloud.ResourceClient {
	return &ciSetupFakeRC{provider: f}
}

func (f *ciSetupFakeProvider) EnsureProject(_ context.Context, _ cloud.CodeBuildProjectSpec) (bool, error) {
	f.ensureCalls++
	return true, nil
}

func (f *ciSetupFakeProvider) DeleteProject(_ context.Context, _ string) error { return nil }

func (f *ciSetupFakeProvider) StartBuild(_ context.Context, _ string, _ map[string]string) (string, error) {
	return "build-1", nil
}

func (f *ciSetupFakeProvider) BuildStatus(_ context.Context, _ string) (cloud.BuildInfo, error) {
	return cloud.BuildInfo{Status: "SUCCEEDED"}, nil
}

func (f *ciSetupFakeProvider) BuildLog(_ context.Context, _ string) (string, error) {
	return "", nil
}

type ciSetupFakeRC struct {
	provider *ciSetupFakeProvider
}

func (r *ciSetupFakeRC) Create(_ context.Context, res *cloud.Resource) error {
	r.provider.createCalls++
	res.Identifier = res.TypeName + "-id"
	return nil
}
func (r *ciSetupFakeRC) Get(_ context.Context, _ *cloud.Resource) error    { return nil }
func (r *ciSetupFakeRC) Update(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *ciSetupFakeRC) Delete(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *ciSetupFakeRC) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}

// identityOnlyProvider satisfies cloud.Provider only (no CodeBuildRunner).
type identityOnlyProvider struct{}

func (identityOnlyProvider) Name() string { return "fake" }
func (identityOnlyProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-east-1", nil
}
func (identityOnlyProvider) Resources() cloud.ResourceClient { return nil }
