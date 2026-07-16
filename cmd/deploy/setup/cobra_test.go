package setup_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/deploy/setup"
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
	root.AddCommand(setup.New(runtimeSource, optionsSource, out))
	return root
}

func runDeploySetup(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
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
	cfg.Deploy.BuildBucket = "fabrica-builds"
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
	provider := &deploySetupFakeProvider{}
	got, err := runDeploySetup(t, newTestRuntime(provider), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "Deploy setup (dry run)")
	assertContains(t, got, "IAM role")
	assertContains(t, got, "GameLift alias")
	assertContains(t, got, "fabrica-builds")
	assertContains(t, got, "Cost estimate")
	if provider.createCalls != 0 {
		t.Errorf("dry-run must not create resources, got %d creates", provider.createCalls)
	}
}

// TestSetupCobraYesCreatesResources runs New→ExecuteContext with --yes.
func TestSetupCobraYesCreatesResources(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	provider := &deploySetupFakeProvider{}
	got, err := runDeploySetup(t, newTestRuntime(provider), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "Deploy setup complete")
	assertContains(t, got, "Next steps:")
	assertContains(t, got, "fabrica deploy promote")
	if provider.createCalls != 2 {
		t.Errorf("expected 2 creates (role + alias), got %d", provider.createCalls)
	}
	if _, err := os.Stat(dir + "/.fabrica/state.json"); err != nil {
		t.Errorf("expected state file after setup: %v", err)
	}
}

// TestSetupCobraRequiresBuildBucket fails when deploy.buildBucket is unset.
func TestSetupCobraRequiresBuildBucket(t *testing.T) {
	t.Chdir(t.TempDir())
	src := func() (globals.Runtime, error) {
		cfg := config.Defaults()
		cfg.Deploy.BuildBucket = ""
		return globals.Runtime{Config: cfg, Provider: &deploySetupFakeProvider{}}, nil
	}
	_, err := runDeploySetup(t, src, "--yes")
	if err == nil {
		t.Fatal("expected error when buildBucket is unset")
	}
	assertContains(t, err.Error(), "buildBucket")
}

// TestSetupCobraNilProvider errors through the real Cobra wiring.
func TestSetupCobraNilProvider(t *testing.T) {
	t.Chdir(t.TempDir())
	src := func() (globals.Runtime, error) {
		cfg := config.Defaults()
		cfg.Deploy.BuildBucket = "bkt"
		return globals.Runtime{Config: cfg, Provider: nil}, nil
	}
	_, err := runDeploySetup(t, src, "--yes")
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
	_, err := runDeploySetup(t, src)
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// ---- fakes ----

type deploySetupFakeProvider struct {
	createCalls int
}

func (f *deploySetupFakeProvider) Name() string { return "fake" }

func (f *deploySetupFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *deploySetupFakeProvider) Resources() cloud.ResourceClient {
	return &deploySetupFakeRC{provider: f}
}

type deploySetupFakeRC struct {
	provider *deploySetupFakeProvider
}

func (r *deploySetupFakeRC) Create(_ context.Context, res *cloud.Resource) error {
	r.provider.createCalls++
	res.Identifier = res.TypeName + "-id"
	return nil
}
func (r *deploySetupFakeRC) Get(_ context.Context, _ *cloud.Resource) error    { return nil }
func (r *deploySetupFakeRC) Update(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *deploySetupFakeRC) Delete(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *deploySetupFakeRC) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}
