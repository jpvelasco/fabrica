package state

import (
	"context"
	"fmt"

	fabricac "github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

// BootstrapResult describes the outcome of one bootstrapping step.
type BootstrapResult struct {
	Name    string
	Existed bool
}

func (r BootstrapResult) String() string {
	if r.Existed {
		return fmt.Sprintf("  %s already exists — skipping", r.Name)
	}
	return fmt.Sprintf("  created %s", r.Name)
}

func Bootstrap(ctx context.Context, provider fabricac.Provider, cfg *config.Config) ([]BootstrapResult, error) {
	account, _, region, err := provider.Identity(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolving identity: %w", err)
	}

	backend := ApplyBackendNames(cfg, account)

	var results []BootstrapResult

	// 1. S3 bucket (idempotent: AlreadyExists → success)
	result, err := createBucket(ctx, backend.Bucket)
	if err != nil {
		return results, fmt.Errorf("creating S3 bucket: %w", err)
	}
	results = append(results, result)

	if !result.Existed {
		// 2-4. Bucket config (only on fresh creation)
		for _, r := range []struct {
			fn   string
			do   func() error
			name string
		}{
			{"versioning", func() error { return enableVersioning(ctx, backend.Bucket) }, "S3 bucket versioning"},
			{"public access block", func() error { return blockPublicAccess(ctx, backend.Bucket) }, "S3 public access block"},
			{"encryption", func() error { return enableEncryption(ctx, backend.Bucket, cfg.State.KMSKeyID) }, "S3 bucket encryption"},
		} {
			if err := r.do(); err != nil {
				return results, fmt.Errorf("%s: %w", r.fn, err)
			}
			results = append(results, BootstrapResult{Name: r.name})
		}
	}

	// 5. DynamoDB lock table (idempotent: ResourceInUse → success)
	result, err = createTable(ctx, backend.Table, region)
	if err != nil {
		return results, fmt.Errorf("creating DynamoDB lock table: %w", err)
	}
	results = append(results, result)

	return results, nil
}

// createBucket attempts to create the S3 bucket. Returns existed=true
// if the bucket already exists (already handled gracefully).
func createBucket(ctx context.Context, bucket string) (BootstrapResult, error) {
	// TODO: implement via direct SDK call after AWS provider bootstrap client is wired
	return BootstrapResult{Name: fmt.Sprintf("S3 bucket %s", bucket), Existed: true}, nil
}

// enableVersioning enables versioning on the bucket.
func enableVersioning(ctx context.Context, bucket string) error {
	// TODO: implement
	return nil
}

// blockPublicAccess applies a public-access block to the bucket.
func blockPublicAccess(ctx context.Context, bucket string) error {
	// TODO: implement
	return nil
}

// enableEncryption enables SSE-S3 encryption (or SSE-KMS if kmsKeyID is set).
func enableEncryption(ctx context.Context, bucket, kmsKeyID string) error {
	// TODO: implement
	_ = kmsKeyID
	return nil
}

// createTable attempts to create the DynamoDB lock table. Returns existed=true
// if the table already exists.
func createTable(ctx context.Context, table, region string) (BootstrapResult, error) {
	// TODO: implement via direct SDK call
	return BootstrapResult{Name: fmt.Sprintf("DynamoDB lock table %s", table), Existed: true}, nil
}
