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
		confirm: func(string) bool {
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
	assertContains(t, out.String(), "Dry run: no resources will be deleted.")
}

func TestDestroyAbortDoesNotDelete(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	cmd := command{
		runtime: globals.Runtime{
			Config:   destroyTestConfig(),
			Provider: provider,
		},
		all: true,
		out: &out,
		confirm: func(msg string) bool {
			if msg != "Continue with destroy?" {
				t.Fatalf("confirm message = %q", msg)
			}
			return false
		},
	}

	if err := cmd.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.deletedBucket || provider.deletedTable {
		t.Fatal("aborted destroy deleted resources")
	}
	assertContains(t, out.String(), "Aborted.")
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
	assertContains(t, out.String(), "Proceeding (--yes flag set).")
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
	assertContains(t, out.String(), "S3 bucket fabrica-state-test not found; skipping")
	assertContains(t, out.String(), "DynamoDB lock table fabrica-locks-test not found; skipping")
}

func TestDestroyStopsBeforeTableWhenBucketDeleteFails(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{bucketErr: fmt.Errorf("bucket not empty")}
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
}

func destroyTestConfig() *config.Config {
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.State.Table = "fabrica-locks-test"
	return cfg
}

type fakeProvider struct {
	bucket        string
	table         string
	deletedBucket bool
	deletedTable  bool
	bucketMissing bool
	tableMissing  bool
	bucketErr     error
	tableErr      error
}

func (f *fakeProvider) Name() string {
	return "fake"
}

func (f *fakeProvider) Identity(ctx context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}

func (f *fakeProvider) Resources() cloud.ResourceClient {
	return nil
}

func (f *fakeProvider) DeleteStateBucket(ctx context.Context, bucket string) (cloud.StateBackendDeleteResult, error) {
	f.bucket = bucket
	if f.bucketErr != nil {
		return cloud.StateBackendDeleteResult{Identifier: bucket}, f.bucketErr
	}
	if f.bucketMissing {
		return cloud.StateBackendDeleteResult{Identifier: bucket, Missing: true}, nil
	}
	f.deletedBucket = true
	return cloud.StateBackendDeleteResult{Identifier: bucket, Deleted: true}, nil
}

func (f *fakeProvider) DeleteStateLockTable(ctx context.Context, table string) (cloud.StateBackendDeleteResult, error) {
	f.table = table
	if f.tableErr != nil {
		return cloud.StateBackendDeleteResult{Identifier: table}, f.tableErr
	}
	if f.tableMissing {
		return cloud.StateBackendDeleteResult{Identifier: table, Missing: true}, nil
	}
	f.deletedTable = true
	return cloud.StateBackendDeleteResult{Identifier: table, Deleted: true}, nil
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
