//go:build integration

// Integration test for the real AWS state backend bootstrapper.
//
// Run with real AWS credentials:
//
//	FABRICA_INTEGRATION=1 FABRICA_INTEGRATION_ACCOUNT=<acct> AWS_REGION=us-west-2 \
//	  go test -tags integration ./internal/cloud/aws/ -run TestIntegrationBootstrap -v
//
// It creates a uniquely-named S3 bucket and DynamoDB table (the bucket holds no
// objects), asserts they exist, verifies idempotency, and unconditionally
// cleans both up via t.Cleanup. Excluded from the default test run and CI.
package aws

import (
	"context"
	"os"
	"testing"
	"time"

	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestIntegrationBootstrap(t *testing.T) {
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

	boot, ok := prov.(fabricac.StateBackendBootstrapper)
	if !ok {
		t.Fatal("provider does not implement StateBackendBootstrapper")
	}
	checker := prov.(fabricac.StateBackendChecker)
	destroyer := prov.(fabricac.StateBackendDestroyer)

	suffix := time.Now().UTC().Format("20060102150405")
	bucket := "fabrica-it-" + account + "-" + suffix
	table := "fabrica-it-lock-" + suffix

	t.Cleanup(func() {
		// Best-effort unconditional cleanup. The bucket holds no objects, so
		// DeleteBucket succeeds without an empty step.
		_, _ = destroyer.DeleteStateBucket(context.Background(), bucket)
		_, _ = destroyer.DeleteStateLockTable(context.Background(), table)
	})

	if _, err := boot.EnsureStateBucket(ctx, bucket, region); err != nil {
		t.Fatalf("EnsureStateBucket: %v", err)
	}
	if _, err := boot.EnsureStateLockTable(ctx, table); err != nil {
		t.Fatalf("EnsureStateLockTable: %v", err)
	}

	ok1, err := checker.StateBucketExists(ctx, bucket)
	if err != nil || !ok1 {
		t.Fatalf("bucket should exist: ok=%v err=%v", ok1, err)
	}
	ok2, err := checker.StateLockTableExists(ctx, table)
	if err != nil || !ok2 {
		t.Fatalf("table should exist: ok=%v err=%v", ok2, err)
	}

	// Idempotency: a second call must not error and must report not-created.
	res, err := boot.EnsureStateBucket(ctx, bucket, region)
	if err != nil {
		t.Fatalf("second EnsureStateBucket: %v", err)
	}
	if res.Created {
		t.Error("second EnsureStateBucket should report Created=false")
	}
	tableRes, err := boot.EnsureStateLockTable(ctx, table)
	if err != nil {
		t.Fatalf("second EnsureStateLockTable: %v", err)
	}
	if tableRes.Created {
		t.Error("second EnsureStateLockTable should report Created=false")
	}
}
