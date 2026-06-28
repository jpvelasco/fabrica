package setup_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/setup"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

// buildTestRoot wires a minimal root replicating the persistent-flag hierarchy
// (--dry-run and --yes live on root, not on the subcommand).
func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) (*cobra.Command, *globals.Options) {
	opts := &globals.Options{}
	root := &cobra.Command{
		Use:           "fabrica",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "")
	root.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "")
	root.SetOut(out)
	root.SetErr(out)

	optionsSource := func() globals.Options { return *opts }
	root.AddCommand(setup.New(runtimeSource, optionsSource, out))
	return root, opts
}

func runSetup(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root, _ := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"setup"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func newCobraRuntime(provider cloud.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	cfg.State.Bucket = "fabrica-state-123456789012"
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

// TestSetupCobraDryRun verifies --dry-run prints the plan + cost and never bootstraps.
func TestSetupCobraDryRun(t *testing.T) {
	got, err := runSetup(t, newCobraRuntime(&cobraFakeProvider{}), "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "Setup (dry run)")
	assertContains(t, got, "Cost estimate:")
	assertContains(t, got, "Run without --dry-run to create these resources.")
}

// TestSetupCobraYesApplies verifies --yes drives a successful apply with no prompt.
func TestSetupCobraYesApplies(t *testing.T) {
	t.Chdir(t.TempDir()) // saveAccountID is a no-op (account preset); guard any writes
	got, err := runSetup(t, newCobraRuntime(&cobraFakeProvider{}), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertContains(t, got, "Proceeding without confirmation")
	assertContains(t, got, "Setup complete")
}

// TestSetupCobraRuntimeError verifies runtimeSource errors surface as command errors.
func TestSetupCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config not loaded")
	}
	if _, err := runSetup(t, src, "--dry-run"); err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
}

// ---- cobraFakeProvider implements cloud.Provider + StateBackendBootstrapper ----

type cobraFakeProvider struct{}

func (f *cobraFakeProvider) Name() string { return "fake" }
func (f *cobraFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}
func (f *cobraFakeProvider) Resources() cloud.ResourceClient { return nil }
func (f *cobraFakeProvider) EnsureStateBucket(_ context.Context, bucket, _ string) (cloud.StateBackendCreateResult, error) {
	return cloud.StateBackendCreateResult{Identifier: bucket, Created: true}, nil
}
func (f *cobraFakeProvider) EnsureStateLockTable(_ context.Context, table string) (cloud.StateBackendCreateResult, error) {
	return cloud.StateBackendCreateResult{Identifier: table, Created: true}, nil
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
