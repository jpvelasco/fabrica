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

// Bootstrap creates the S3 state bucket and DynamoDB lock table for this account.
// The identity check runs first so credential problems surface immediately, then
// the bucket and table are created in order (bucket first). Each step is
// idempotent: an already-existing resource is reported with Existed=true, and a
// re-run reconciles configuration rather than failing.
func Bootstrap(ctx context.Context, provider fabricac.Provider, cfg *config.Config) ([]BootstrapResult, error) {
	account, _, region, err := provider.Identity(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolving identity: %w", err)
	}

	bootstrapper, ok := provider.(fabricac.StateBackendBootstrapper)
	if !ok {
		return nil, fmt.Errorf("provider %s does not support state backend bootstrap — create the S3 bucket and DynamoDB table manually", provider.Name())
	}

	names := ResolveBackendNames(cfg, account)

	bucket, err := bootstrapper.EnsureStateBucket(ctx, names.Bucket, region)
	if err != nil {
		return nil, fmt.Errorf("creating state bucket %s: %w", names.Bucket, err)
	}

	table, err := bootstrapper.EnsureStateLockTable(ctx, names.Table)
	if err != nil {
		return nil, fmt.Errorf("creating lock table %s (state bucket %s was handled): %w", names.Table, names.Bucket, err)
	}

	return []BootstrapResult{
		{Name: "S3 bucket " + bucket.Identifier, Existed: !bucket.Created},
		{Name: "DynamoDB table " + table.Identifier, Existed: !table.Created},
	}, nil
}
