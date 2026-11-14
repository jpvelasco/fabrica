package e2e

import (
	"context"
	"fmt"
	"maps"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/tags"
)

// backendData is used only while the store mutex is held. Existence and actual
// tags remain independent so boolean-only fixtures cannot manufacture ownership.
func (p *fakeProvider) backendData(bucket bool) (map[string]bool, map[string]map[string]string) {
	if bucket {
		return p.store.buckets, p.store.bucketTags
	}
	return p.store.tables, p.store.tableTags
}

func (p *fakeProvider) backendMetadata(ctx context.Context, bucket bool, name string) (cloud.StateBackendMetadata, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return cloud.StateBackendMetadata{}, err
	}
	existence, observed := p.backendData(bucket)
	if !existence[name] {
		return cloud.StateBackendMetadata{}, nil
	}
	return cloud.StateBackendMetadata{Exists: true, Tags: maps.Clone(observed[name])}, nil
}

func (p *fakeProvider) StateBucketMetadata(ctx context.Context, bucket string) (cloud.StateBackendMetadata, error) {
	return p.backendMetadata(ctx, true, bucket)
}
func (p *fakeProvider) StateLockTableMetadata(ctx context.Context, table string) (cloud.StateBackendMetadata, error) {
	return p.backendMetadata(ctx, false, table)
}

func (p *fakeProvider) createBackend(ctx context.Context, bucket bool, name string, desired map[string]string) (cloud.StateBackendCreateResult, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	result := cloud.StateBackendCreateResult{Identifier: name}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	existence, observed := p.backendData(bucket)
	if existence[name] {
		return result, fmt.Errorf("%s appeared; repeat the full preflight", name)
	}
	existence[name], observed[name] = true, maps.Clone(desired)
	result.Created = true
	return result, nil
}

func (p *fakeProvider) CreateStateBucket(ctx context.Context, bucket string, spec cloud.StateBucketCreateSpec) (cloud.StateBackendCreateResult, error) {
	if spec.StateDocumentKey == "" {
		return cloud.StateBackendCreateResult{Identifier: bucket}, fmt.Errorf("canonical document key is required")
	}
	return p.createBackend(ctx, true, bucket, spec.Tags)
}
func (p *fakeProvider) CreateStateLockTable(ctx context.Context, table string, desired map[string]string) (cloud.StateBackendCreateResult, error) {
	return p.createBackend(ctx, false, table, desired)
}

func (p *fakeProvider) reconcileBackend(ctx context.Context, bucket bool, name string, spec cloud.StateBackendReconcileSpec) error {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	existence, observed := p.backendData(bucket)
	if !existence[name] || !spec.Expected.Exists {
		return fmt.Errorf("%s disappeared or was not approved existing; repeat the full preflight", name)
	}
	actual := observed[name]
	for _, key := range []string{tags.ManagedByKey, tags.OwnershipVersionKey} {
		before, expected := spec.Expected.Tags[key]
		value, present := actual[key]
		if before != value || expected != present {
			return fmt.Errorf("%s protected snapshot changed; repeat the full preflight", name)
		}
	}
	marker, present := actual[tags.OwnershipVersionKey]
	if desired, supplied := spec.Tags[tags.OwnershipVersionKey]; supplied && (!present || desired != marker) {
		return fmt.Errorf("%s existing marker must be preserved", name)
	}
	merged := make(map[string]string)
	maps.Copy(merged, actual)
	maps.Copy(merged, spec.Tags)
	observed[name] = merged
	return nil
}

func (p *fakeProvider) ReconcileStateBucket(ctx context.Context, bucket string, spec cloud.StateBackendReconcileSpec) error {
	return p.reconcileBackend(ctx, true, bucket, spec)
}
func (p *fakeProvider) ReconcileStateLockTable(ctx context.Context, table string, spec cloud.StateBackendReconcileSpec) error {
	return p.reconcileBackend(ctx, false, table, spec)
}

var (
	_ cloud.StateBackendMetadataReader = (*fakeProvider)(nil)
	_ cloud.StateBackendCreator        = (*fakeProvider)(nil)
	_ cloud.StateBackendReconciler     = (*fakeProvider)(nil)
)
