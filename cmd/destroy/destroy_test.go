package destroy

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestDestroyDryRunDoesNotDelete(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	cmd := command{
		runtime: globals.Runtime{
			Config:   destroyTestConfig(),
			Provider: provider,
		},
		all:    true,
		dryRun: true,
		out:    &out,
		confirm: func(string, string) bool {
			t.Fatal("confirm should not be called during dry run")
			return false
		},
	}

	if err := cmd.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.deletedBucket || provider.deletedTable {
		t.Fatal("dry run deleted resources")
	}
	assertContains(t, out.String(), "No resources will be deleted. No AWS delete calls will be made.")
}

func TestDestroyDryRunPrintsStrongWarning(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	cmd := command{
		runtime: globals.Runtime{
			Config:   destroyTestConfig(),
			Provider: provider,
		},
		all:    true,
		dryRun: true,
		out:    &out,
	}

	if err := cmd.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	assertContains(t, got, "Destroy dry run")
	assertContains(t, got, "No resources will be deleted. No AWS delete calls will be made.")
	assertContains(t, got, "Resources that would be deleted:")
	assertContains(t, got, "AWS account ID: 123456789012")
	assertContains(t, got, "AWS region:     us-east-1")
	assertContains(t, got, "S3 state bucket:      fabrica-state-test")
	assertContains(t, got, "DynamoDB lock table:  fabrica-locks-test")
	assertContains(t, got, "Deletion order if run for real:")
}

func TestDestroyConfirmationRequiresExactAccountAndBucketPhrase(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	var gotMsg string
	var gotPhrase string
	cmd := command{
		runtime: globals.Runtime{
			Config:   destroyTestConfig(),
			Provider: provider,
		},
		all: true,
		out: &out,
		confirm: func(msg, phrase string) bool {
			gotMsg = msg
			gotPhrase = phrase
			return false
		},
	}

	if err := cmd.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.deletedBucket || provider.deletedTable {
		t.Fatal("aborted destroy deleted resources")
	}
	if gotPhrase != "destroy 123456789012 fabrica-state-test" {
		t.Fatalf("confirmation phrase = %q, want destroy 123456789012 fabrica-state-test", gotPhrase)
	}
	if gotMsg != "Enter confirmation phrase" {
		t.Fatalf("confirmation prompt = %q, want Enter confirmation phrase", gotMsg)
	}
	got := out.String()
	assertContains(t, got, "Final confirmation required.")
	assertContains(t, got, "Type this exact phrase to continue:")
	assertContains(t, got, "  destroy 123456789012 fabrica-state-test")
	assertContains(t, got, "Any other input cancels destroy.")
	assertContains(t, got, "Destroy cancelled: confirmation phrase did not match.")
	assertContains(t, out.String(), "No AWS delete calls were made.")
}

func TestDestroyDeletesAfterExactConfirmation(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	cmd := command{
		runtime: globals.Runtime{
			Config:   destroyTestConfig(),
			Provider: provider,
		},
		all: true,
		out: &out,
		confirm: func(msg, phrase string) bool {
			return phrase == "destroy 123456789012 fabrica-state-test"
		},
	}

	if err := cmd.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !provider.deletedBucket || !provider.deletedTable {
		t.Fatal("confirmed destroy did not delete both resources")
	}
	assertContains(t, out.String(), "Confirmation accepted.")
}

func TestDestroyCanRunTwiceSafely(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	cmd := command{
		runtime: globals.Runtime{
			Config:   destroyTestConfig(),
			Provider: provider,
		},
		all:       true,
		assumeYes: true,
		out:       &out,
	}

	if err := cmd.run(context.Background()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := cmd.run(context.Background()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if provider.bucketDeleteCalls != 2 {
		t.Fatalf("bucket delete calls = %d, want 2", provider.bucketDeleteCalls)
	}
	if provider.tableDeleteCalls != 2 {
		t.Fatalf("table delete calls = %d, want 2", provider.tableDeleteCalls)
	}
	got := out.String()
	assertContains(t, got, "deleted S3 state bucket: fabrica-state-test")
	assertContains(t, got, "S3 state bucket not found; skipping: fabrica-state-test")
	assertContains(t, got, "DynamoDB lock table not found; skipping: fabrica-locks-test")
}

func TestDestroyDeletesPhase0Backend(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	cmd := command{
		runtime: globals.Runtime{
			Config:   destroyTestConfig(),
			Provider: provider,
		},
		all:       true,
		assumeYes: true,
		out:       &out,
	}

	if err := cmd.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !provider.deletedBucket {
		t.Fatal("bucket was not deleted")
	}
	if !provider.deletedTable {
		t.Fatal("table was not deleted")
	}
	if provider.bucket != "fabrica-state-test" {
		t.Fatalf("bucket = %q, want fabrica-state-test", provider.bucket)
	}
	if provider.table != "fabrica-locks-test" {
		t.Fatalf("table = %q, want fabrica-locks-test", provider.table)
	}
	assertContains(t, out.String(), "Proceeding without interactive confirmation (--yes flag set).")
	assertContains(t, out.String(), "Use --yes only in automation you trust.")
	assertContains(t, out.String(), "Destroy complete.")
}

func TestDestroyTreatsMissingResourcesAsSuccess(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{bucketMissing: true, tableMissing: true}
	cmd := command{
		runtime: globals.Runtime{
			Config:   destroyTestConfig(),
			Provider: provider,
		},
		all:       true,
		assumeYes: true,
		out:       &out,
	}

	if err := cmd.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "S3 state bucket not found; skipping: fabrica-state-test")
	assertContains(t, out.String(), "DynamoDB lock table not found; skipping: fabrica-locks-test")
}

func TestDestroyContinuesWhenBucketIsAlreadyMissing(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{bucketMissing: true}
	cmd := command{
		runtime: globals.Runtime{
			Config:   destroyTestConfig(),
			Provider: provider,
		},
		all:       true,
		assumeYes: true,
		out:       &out,
	}

	if err := cmd.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.bucketDeleteCalls != 1 {
		t.Fatalf("bucket delete calls = %d, want 1", provider.bucketDeleteCalls)
	}
	if provider.tableDeleteCalls != 1 {
		t.Fatalf("table delete calls = %d, want 1", provider.tableDeleteCalls)
	}
	if !provider.deletedTable {
		t.Fatal("table should be deleted when bucket is already missing")
	}
	assertContains(t, out.String(), "S3 state bucket not found; skipping: fabrica-state-test")
	assertContains(t, out.String(), "deleted DynamoDB lock table: fabrica-locks-test")
}

func TestDestroyStopsBeforeTableWhenBucketDeleteFails(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{bucketErr: cloud.ErrStateBucketNotEmpty}
	cmd := command{
		runtime: globals.Runtime{
			Config:   destroyTestConfig(),
			Provider: provider,
		},
		all:       true,
		assumeYes: true,
		out:       &out,
	}

	err := cmd.run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if provider.deletedTable {
		t.Fatal("table should not be deleted after bucket failure")
	}
	assertContains(t, out.String(), "failed to delete S3 state bucket: fabrica-state-test")
	assertContains(t, out.String(), "Empty all objects, object versions, and delete markers")
	assertContains(t, out.String(), "DynamoDB lock table deletion was not attempted.")
}

func TestDestroyReportsGenericBucketDeleteFailure(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{bucketErr: fmt.Errorf("access denied")}
	cmd := command{
		runtime: globals.Runtime{
			Config:   destroyTestConfig(),
			Provider: provider,
		},
		all:       true,
		assumeYes: true,
		out:       &out,
	}

	err := cmd.run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if provider.tableDeleteCalls != 0 {
		t.Fatal("table deletion should not be attempted after bucket failure")
	}
	assertContains(t, err.Error(), "destroy failed before deleting DynamoDB lock table")
	assertContains(t, out.String(), "failed to delete S3 state bucket: fabrica-state-test")
	assertContains(t, out.String(), "Error: access denied")
}

func TestDestroyReportsTableDeleteFailureAsPartialCompletion(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{tableErr: fmt.Errorf("access denied")}
	cmd := command{
		runtime: globals.Runtime{
			Config:   destroyTestConfig(),
			Provider: provider,
		},
		all:       true,
		assumeYes: true,
		out:       &out,
	}

	err := cmd.run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !provider.deletedBucket {
		t.Fatal("bucket should be handled before table failure")
	}
	assertContains(t, err.Error(), "destroy partially completed")
	assertContains(t, out.String(), "failed to delete DynamoDB lock table: fabrica-locks-test")
}

func TestDestroyWithoutAllPrintsUsageHint(t *testing.T) {
	var out bytes.Buffer
	cmd := command{out: &out}

	if err := cmd.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "To destroy infrastructure, use --all:")
}

func TestDestroyUnsupportedProviderFailsBeforeConfirmation(t *testing.T) {
	var out bytes.Buffer
	provider := unsupportedProvider{}
	cmd := command{
		runtime: globals.Runtime{
			Config:   destroyTestConfig(),
			Provider: provider,
		},
		all:       true,
		assumeYes: true,
		out:       &out,
	}

	err := cmd.run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	assertContains(t, err.Error(), "provider unsupported does not support state backend destroy")
}

func TestDestroyIdentityFailureSkipsConfirmationAndDeletion(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{identityErr: fmt.Errorf("identity unavailable")}
	cmd := command{
		runtime: globals.Runtime{
			Config:   destroyTestConfig(),
			Provider: provider,
		},
		all: true,
		out: &out,
		confirm: func(string, string) bool {
			t.Fatal("confirm should not be called when identity resolution fails")
			return false
		},
	}

	err := cmd.run(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if provider.bucketDeleteCalls != 0 || provider.tableDeleteCalls != 0 {
		t.Fatal("delete calls should not be made when identity resolution fails")
	}
	assertContains(t, err.Error(), "resolving identity")
	assertContains(t, err.Error(), "identity unavailable")
}

func destroyTestConfig() *config.Config {
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.State.Table = "fabrica-locks-test"
	return cfg
}

type fakeProvider struct {
	bucket            string
	table             string
	deletedBucket     bool
	deletedTable      bool
	bucketMissing     bool
	tableMissing      bool
	identityErr       error
	bucketErr         error
	tableErr          error
	bucketDeleteCalls int
	tableDeleteCalls  int
}

func (f *fakeProvider) Name() string {
	return "fake"
}

func (f *fakeProvider) Identity(ctx context.Context) (string, string, string, error) {
	if f.identityErr != nil {
		return "", "", "", f.identityErr
	}
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *fakeProvider) Resources() cloud.ResourceClient {
	return nil
}

func (f *fakeProvider) DeleteStateBucket(ctx context.Context, bucket string) (cloud.StateBackendDeleteResult, error) {
	f.bucketDeleteCalls++
	f.bucket = bucket
	if f.bucketErr != nil {
		return cloud.StateBackendDeleteResult{Identifier: bucket}, f.bucketErr
	}
	if f.bucketMissing || f.deletedBucket {
		return cloud.StateBackendDeleteResult{Identifier: bucket, Missing: true}, nil
	}
	f.deletedBucket = true
	return cloud.StateBackendDeleteResult{Identifier: bucket, Deleted: true}, nil
}

func (f *fakeProvider) DeleteStateLockTable(ctx context.Context, table string) (cloud.StateBackendDeleteResult, error) {
	f.tableDeleteCalls++
	f.table = table
	if f.tableErr != nil {
		return cloud.StateBackendDeleteResult{Identifier: table}, f.tableErr
	}
	if f.tableMissing || f.deletedTable {
		return cloud.StateBackendDeleteResult{Identifier: table, Missing: true}, nil
	}
	f.deletedTable = true
	return cloud.StateBackendDeleteResult{Identifier: table, Deleted: true}, nil
}

type unsupportedProvider struct{}

func (unsupportedProvider) Name() string {
	return "unsupported"
}

func (unsupportedProvider) Identity(ctx context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (unsupportedProvider) Resources() cloud.ResourceClient {
	return nil
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
