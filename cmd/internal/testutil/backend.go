package testutil

import (
	"context"
	"fmt"
	"maps"
	"strconv"
	"sync"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/tags"
)

// BackendCall records actual provider operation, target and context arguments.
type BackendCall struct {
	Operation, Identifier string
	Context               context.Context
}

// BackendProvider adds observed backend mechanics without giving TestProvider
// these capabilities. Empty observed tags remain untagged; no owner is inferred.
type BackendProvider struct {
	*TestProvider
	Bucket, Table cloud.StateBackendMetadata
	BackendErr    map[string]error
	BackendCalls  []BackendCall
	mu            sync.Mutex
}

var (
	_ cloud.StateBackendMetadataReader = (*BackendProvider)(nil)
	_ cloud.StateBackendCreator        = (*BackendProvider)(nil)
	_ cloud.StateBackendReconciler     = (*BackendProvider)(nil)
	_ cloud.StateStore                 = (*BackendProvider)(nil)
	_ cloud.StateLockManager           = (*BackendLockingProvider)(nil)
)

// record is called under mu so heartbeat and command evidence stay synchronized.
func (p *BackendProvider) record(ctx context.Context, operation, name string) error {
	p.BackendCalls = append(p.BackendCalls, BackendCall{operation, name, ctx})
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.BackendErr[operation]
}

func (p *BackendProvider) metadata(ctx context.Context, operation, name string, component *cloud.StateBackendMetadata) (cloud.StateBackendMetadata, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.record(ctx, operation, name); err != nil {
		return cloud.StateBackendMetadata{}, err
	}
	return cloud.StateBackendMetadata{Exists: component.Exists, Tags: maps.Clone(component.Tags)}, nil
}

func (p *BackendProvider) StateBucketMetadata(ctx context.Context, bucket string) (cloud.StateBackendMetadata, error) {
	return p.metadata(ctx, "bucket metadata", bucket, &p.Bucket)
}
func (p *BackendProvider) StateLockTableMetadata(ctx context.Context, table string) (cloud.StateBackendMetadata, error) {
	return p.metadata(ctx, "table metadata", table, &p.Table)
}

func (p *BackendProvider) create(ctx context.Context, operation, name string, component *cloud.StateBackendMetadata, desired map[string]string) (cloud.StateBackendCreateResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := cloud.StateBackendCreateResult{Identifier: name}
	if err := p.record(ctx, operation, name); err != nil {
		return result, err
	}
	if component.Exists {
		return result, fmt.Errorf("%s appeared; repeat the full preflight", name)
	}
	*component = cloud.StateBackendMetadata{Exists: true, Tags: maps.Clone(desired)}
	result.Created = true
	return result, nil
}

func (p *BackendProvider) CreateStateBucket(ctx context.Context, bucket string, spec cloud.StateBucketCreateSpec) (cloud.StateBackendCreateResult, error) {
	if spec.StateDocumentKey == "" {
		return cloud.StateBackendCreateResult{Identifier: bucket}, fmt.Errorf("canonical document key is required")
	}
	return p.create(ctx, "create bucket", bucket, &p.Bucket, spec.Tags)
}
func (p *BackendProvider) CreateStateLockTable(ctx context.Context, table string, desired map[string]string) (cloud.StateBackendCreateResult, error) {
	return p.create(ctx, "create table", table, &p.Table, desired)
}

func (p *BackendProvider) reconcile(ctx context.Context, operation, name string, component *cloud.StateBackendMetadata, spec cloud.StateBackendReconcileSpec) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.record(ctx, operation, name); err != nil {
		return err
	}
	if !component.Exists || !spec.Expected.Exists {
		return fmt.Errorf("%s disappeared or was not approved existing; repeat the full preflight", name)
	}
	for _, key := range []string{tags.ManagedByKey, tags.OwnershipVersionKey} {
		before, expected := spec.Expected.Tags[key]
		actual, present := component.Tags[key]
		if before != actual || expected != present {
			return fmt.Errorf("%s protected snapshot changed; repeat the full preflight", name)
		}
	}
	marker, present := component.Tags[tags.OwnershipVersionKey]
	if desired, supplied := spec.Tags[tags.OwnershipVersionKey]; supplied && (!present || desired != marker) {
		return fmt.Errorf("%s existing marker must be preserved", name)
	}
	merged := make(map[string]string)
	maps.Copy(merged, component.Tags)
	maps.Copy(merged, spec.Tags)
	component.Tags = merged
	return nil
}

func (p *BackendProvider) ReconcileStateBucket(ctx context.Context, bucket string, spec cloud.StateBackendReconcileSpec) error {
	return p.reconcile(ctx, "reconcile bucket", bucket, &p.Bucket, spec)
}
func (p *BackendProvider) ReconcileStateLockTable(ctx context.Context, table string, spec cloud.StateBackendReconcileSpec) error {
	return p.reconcile(ctx, "reconcile table", table, &p.Table, spec)
}

func (p *BackendProvider) GetStateDocument(ctx context.Context, bucket, key string) ([]byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.record(ctx, "get document", bucket+"/"+key); err != nil {
		return nil, err
	}
	if !p.Bucket.Exists {
		return nil, cloud.ErrStateDocumentMissing
	}
	return p.TestProvider.GetStateDocument(ctx, bucket, key)
}
func (p *BackendProvider) PutStateDocument(ctx context.Context, bucket, key string, data []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.record(ctx, "put document", bucket+"/"+key); err != nil {
		return err
	}
	if !p.Bucket.Exists {
		return cloud.ErrStateBackendAbsent
	}
	return p.TestProvider.PutStateDocument(ctx, bucket, key, data)
}

// BackendLockingProvider models real conditional rows, not a held-context flag.
// provision.AcquireStateLock remains the sole owner of the acquisition context.
type BackendLockingProvider struct {
	*BackendProvider
	rows map[string]map[string]string
}

// LockRow returns a synchronized owned snapshot for actual row assertions.
func (p *BackendLockingProvider) LockRow(table, id string) map[string]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return maps.Clone(p.rows[table+"/"+id])
}

func (p *BackendLockingProvider) AcquireStateLockRow(ctx context.Context, table string, item map[string]string, condition string, values map[string]string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.record(ctx, "acquire row", table+"/"+item["LockID"]); err != nil {
		return err
	}
	if !p.Table.Exists {
		return cloud.ErrLockTableMissing
	}
	key := table + "/" + item["LockID"]
	actual := p.rows[key]
	switch condition {
	case state.RenewCondition:
		if actual == nil || actual["Token"] != values[":token"] {
			return cloud.ErrLockHeld
		}
	case "attribute_not_exists(LockID) OR AcquiredAt <= :stale":
		if actual != nil {
			acquired, acquiredErr := strconv.ParseInt(actual["AcquiredAt"], 10, 64)
			stale, staleErr := strconv.ParseInt(values[":stale"], 10, 64)
			if acquiredErr != nil || staleErr != nil {
				return fmt.Errorf("invalid lock ages: acquired=%q, stale=%q", actual["AcquiredAt"], values[":stale"])
			}
			if acquired > stale {
				return cloud.ErrLockHeld
			}
		}
	default:
		return fmt.Errorf("unsupported lock condition %q", condition)
	}
	if p.rows == nil {
		p.rows = make(map[string]map[string]string)
	}
	p.rows[key] = maps.Clone(item)
	return nil
}

func (p *BackendLockingProvider) ReleaseStateLockRow(ctx context.Context, table, id, token string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.record(ctx, "release row", table+"/"+id); err != nil {
		return err
	}
	if !p.Table.Exists {
		return cloud.ErrLockTableMissing
	}
	key := table + "/" + id
	if actual := p.rows[key]; actual == nil || actual["Token"] != token {
		return cloud.ErrLockHeld
	}
	delete(p.rows, key)
	return nil
}
