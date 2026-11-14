//go:build integration

// Integration test for guarded AWS state backend creation and reconciliation.
//
// Run with real AWS credentials:
//
//	FABRICA_INTEGRATION=1 FABRICA_INTEGRATION_ACCOUNT=<acct> AWS_REGION=us-west-2 \
//	  go test -tags integration ./internal/cloud/aws/ -run TestIntegrationStateBackend -v
//
// It creates a uniquely-named S3 bucket and DynamoDB table (the bucket holds no
// objects), verifies expected-absent creation and compatible reconciliation, and
// cleans both up via t.Cleanup. Excluded from the default test run and CI.
package aws

import (
	"context"
	"os"
	"testing"
	"time"

	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/tags"
)

func TestIntegrationStateBackend(t *testing.T) {
	if os.Getenv("FABRICA_INTEGRATION") != "1" {
		t.Skip("set FABRICA_INTEGRATION=1 to run real-AWS integration tests")
	}
	wantAccount := os.Getenv("FABRICA_INTEGRATION_ACCOUNT")
	if wantAccount == "" {
		t.Fatal("FABRICA_INTEGRATION_ACCOUNT must be set to guard against running against the wrong account")
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-west-2"
	}

	cfg := config.Defaults()
	cfg.Cloud.AWS.Region = region
	prov, err := newProvider(cfg)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}

	ctx := context.Background()
	account, _, _, err := prov.Identity(ctx)
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	if account != wantAccount {
		t.Fatalf("refusing to run: live account %s != FABRICA_INTEGRATION_ACCOUNT %s", account, wantAccount)
	}

	creator, ok := prov.(fabricac.StateBackendCreator)
	if !ok {
		t.Fatal("provider does not implement StateBackendCreator")
	}
	reader := prov.(fabricac.StateBackendMetadataReader)
	reconciler := prov.(fabricac.StateBackendReconciler)
	checker := prov.(fabricac.StateBackendChecker)
	destroyer := prov.(fabricac.StateBackendDestroyer)

	suffix := time.Now().UTC().Format("20060102150405")
	bucket := "fabrica-it-" + account + "-" + suffix
	table := "fabrica-it-lock-" + suffix

	var bucketCreated, tableCreated bool
	t.Cleanup(func() {
		// Best-effort cleanup only for components created by this invocation.
		// The bucket holds no objects, so no empty step is needed.
		if bucketCreated {
			_, _ = destroyer.DeleteStateBucket(context.Background(), bucket)
		}
		if tableCreated {
			_, _ = destroyer.DeleteStateLockTable(context.Background(), table)
		}
	})

	desired := tags.Standard("setup", "integration")
	spec := fabricac.StateBucketCreateSpec{Region: region, StateDocumentKey: "state.json", Tags: desired}
	result, err := creator.CreateStateBucket(ctx, bucket, spec)
	bucketCreated = result.Created
	if err != nil || !result.Created {
		t.Fatalf("CreateStateBucket: result=%+v err=%v", result, err)
	}
	result, err = creator.CreateStateLockTable(ctx, table, desired)
	tableCreated = result.Created
	if err != nil || !result.Created {
		t.Fatalf("CreateStateLockTable: result=%+v err=%v", result, err)
	}

	ok1, err := checker.StateBucketExists(ctx, bucket)
	if err != nil || !ok1 {
		t.Fatalf("bucket should exist: ok=%v err=%v", ok1, err)
	}
	ok2, err := checker.StateLockTableExists(ctx, table)
	if err != nil || !ok2 {
		t.Fatalf("table should exist: ok=%v err=%v", ok2, err)
	}

	// Expected-absent creation must refuse existing resources. Existing peers
	// are inspected and reconciled through their separate guarded capability.
	if result, err := creator.CreateStateBucket(ctx, bucket, spec); err == nil || result.Created {
		t.Fatalf("existing bucket creation: result=%+v err=%v", result, err)
	}
	if result, err := creator.CreateStateLockTable(ctx, table, desired); err == nil || result.Created {
		t.Fatalf("existing table creation: result=%+v err=%v", result, err)
	}
	bucketMetadata, err := reader.StateBucketMetadata(ctx, bucket)
	if err != nil || !bucketMetadata.Exists {
		t.Fatalf("bucket metadata: %+v err=%v", bucketMetadata, err)
	}
	tableMetadata, err := reader.StateLockTableMetadata(ctx, table)
	if err != nil || !tableMetadata.Exists {
		t.Fatalf("table metadata: %+v err=%v", tableMetadata, err)
	}
	for key, value := range desired {
		if bucketMetadata.Tags[key] != value || tableMetadata.Tags[key] != value {
			t.Fatalf("created ownership tag %s missing or changed", key)
		}
	}
	if err := reconciler.ReconcileStateBucket(ctx, bucket, fabricac.StateBackendReconcileSpec{Expected: bucketMetadata, Tags: desired}); err != nil {
		t.Fatalf("ReconcileStateBucket: %v", err)
	}
	if err := reconciler.ReconcileStateLockTable(ctx, table, fabricac.StateBackendReconcileSpec{Expected: tableMetadata, Tags: desired}); err != nil {
		t.Fatalf("ReconcileStateLockTable: %v", err)
	}
}
