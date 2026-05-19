package destroy_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/destroy"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

// buildTestRoot constructs a minimal root command that mirrors the production
// flag hierarchy: --dry-run and --yes are persistent flags on root, inherited
// by the destroy subcommand.
func buildTestRoot(runtimeSource globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	var opts globals.Options
	root := &cobra.Command{
		Use:          "fabrica",
		SilenceUsage: true,
		SilenceErrors: true,
	}
	root.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "")
	root.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "")
	root.SetOut(out)
	root.SetErr(out)

	optionsSource := func() globals.Options { return opts }
	root.AddCommand(destroy.New(runtimeSource, optionsSource, out))
	return root
}

// runDestroy builds the command tree, sets args, and executes. Returns
// captured output and any command error.
func runDestroy(t *testing.T, runtimeSource globals.RuntimeSource, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	root := buildTestRoot(runtimeSource, &out)
	root.SetArgs(append([]string{"destroy"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

// newCobraTestRuntime returns a RuntimeSource backed by the given provider with
// a pre-configured config (bucket and table names known to all Cobra tests).
func newCobraTestRuntime(provider cloud.Provider) globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.State.Table = "fabrica-locks-test"
	rt := globals.Runtime{Config: cfg, Provider: provider}
	return func() (globals.Runtime, error) { return rt, nil }
}

// newNilProviderRuntime returns a RuntimeSource with a nil provider
// (simulates "no infrastructure configured").
func newNilProviderRuntime() globals.RuntimeSource {
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.State.Table = "fabrica-locks-test"
	rt := globals.Runtime{Config: cfg, Provider: nil}
	return func() (globals.Runtime, error) { return rt, nil }
}

// TestDestroyCobraNoAllFlag verifies that omitting --all prints the usage
// hint and exits cleanly.
func TestDestroyCobraNoAllFlag(t *testing.T) {
	got, err := runDestroy(t, newCobraTestRuntime(&cobraFakeProvider{}))
	if err != nil {
		t.Fatalf("expected clean exit, got: %v", err)
	}
	assertCobraContains(t, got, "To destroy infrastructure, use --all:")
}

// TestDestroyCobraDryRunNoAWSCalls verifies that --all --dry-run produces
// plan output and makes zero delete calls.
func TestDestroyCobraDryRunNoAWSCalls(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runDestroy(t, newCobraTestRuntime(provider), "--all", "--dry-run")
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	if provider.bucketDeleteCalls != 0 || provider.tableDeleteCalls != 0 {
		t.Fatalf("dry run made AWS calls: bucketDeleteCalls=%d tableDeleteCalls=%d",
			provider.bucketDeleteCalls, provider.tableDeleteCalls)
	}
	assertCobraContains(t, got, "No resources will be deleted. No AWS delete calls will be made.")
}

// TestDestroyCobraDryRunWithNilProvider verifies that --all --dry-run with
// a nil provider exits cleanly (hits the nil-provider guard, no panic).
func TestDestroyCobraDryRunWithNilProvider(t *testing.T) {
	got, err := runDestroy(t, newNilProviderRuntime(), "--all", "--dry-run")
	if err != nil {
		t.Fatalf("expected clean exit with nil provider in dry-run, got: %v", err)
	}
	assertCobraContains(t, got, "No infrastructure found. Nothing to destroy.")
}

// TestDestroyCobraDryRunOutput verifies dry-run output contains all key fields.
func TestDestroyCobraDryRunOutput(t *testing.T) {
	got, err := runDestroy(t, newCobraTestRuntime(&cobraFakeProvider{}), "--all", "--dry-run")
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	checks := []string{
		"Destroy dry run",
		"AWS account ID: 123456789012",
		"AWS region:     us-east-1",
		"S3 state bucket:      fabrica-state-test",
		"DynamoDB lock table:  fabrica-locks-test",
		"Deletion order if run for real:",
		"1. S3 state bucket",
		"2. DynamoDB lock table",
	}
	for _, want := range checks {
		assertCobraContains(t, got, want)
	}
}

// TestDestroyCobraYesFlagPerformsDeletion verifies that --all --yes
// deletes both resources and reports completion.
func TestDestroyCobraYesFlagPerformsDeletion(t *testing.T) {
	provider := &cobraFakeProvider{}
	got, err := runDestroy(t, newCobraTestRuntime(provider), "--all", "--yes")
	if err != nil {
		t.Fatalf("destroy failed: %v", err)
	}
	if !provider.deletedBucket {
		t.Fatal("S3 bucket was not deleted")
	}
	if !provider.deletedTable {
		t.Fatal("DynamoDB table was not deleted")
	}
	assertCobraContains(t, got, "Destroy complete.")
}

// TestDestroyCobraNilProvider verifies that --all --yes with a nil provider
// exits cleanly with an informational message and does not panic.
func TestDestroyCobraNilProvider(t *testing.T) {
	got, err := runDestroy(t, newNilProviderRuntime(), "--all", "--yes")
	if err != nil {
		t.Fatalf("expected clean exit with nil provider, got: %v", err)
	}
	assertCobraContains(t, got, "No infrastructure found. Nothing to destroy.")
}

// TestDestroyCobraIdentityFailurePropagates verifies that an identity
// resolution error surfaces as a command error.
func TestDestroyCobraIdentityFailurePropagates(t *testing.T) {
	provider := &cobraFakeProvider{identityErr: errors.New("credentials unavailable")}
	_, err := runDestroy(t, newCobraTestRuntime(provider), "--all", "--yes")
	if err == nil {
		t.Fatal("expected error when identity fails")
	}
	if !cobraContainsString(err.Error(), "resolving identity") {
		t.Fatalf("error %q does not mention resolving identity", err.Error())
	}
}

// cobraFakeProvider is a minimal fake satisfying cloud.Provider and
// cloud.StateBackendDestroyer for Cobra-layer tests.
type cobraFakeProvider struct {
	deletedBucket     bool
	deletedTable      bool
	identityErr       error
	bucketDeleteCalls int
	tableDeleteCalls  int
}

func (f *cobraFakeProvider) Name() string { return "fake" }

func (f *cobraFakeProvider) Identity(_ context.Context) (string, string, string, error) {
	if f.identityErr != nil {
		return "", "", "", f.identityErr
	}
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *cobraFakeProvider) Resources() cloud.ResourceClient { return nil }

func (f *cobraFakeProvider) DeleteStateBucket(_ context.Context, bucket string) (cloud.StateBackendDeleteResult, error) {
	f.bucketDeleteCalls++
	f.deletedBucket = true
	return cloud.StateBackendDeleteResult{Identifier: bucket, Deleted: true}, nil
}

func (f *cobraFakeProvider) DeleteStateLockTable(_ context.Context, table string) (cloud.StateBackendDeleteResult, error) {
	f.tableDeleteCalls++
	f.deletedTable = true
	return cloud.StateBackendDeleteResult{Identifier: table, Deleted: true}, nil
}

func assertCobraContains(t *testing.T, s, substr string) {
	t.Helper()
	if !cobraContainsString(s, substr) {
		t.Fatalf("%q does not contain %q", s, substr)
	}
}

func cobraContainsString(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
