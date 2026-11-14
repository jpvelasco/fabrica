package testutil_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/provision"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/tags"
)

func TestBackendProviderComponentContracts(t *testing.T) {
	var generic cloud.Provider = &testutil.TestProvider{}
	if _, ok := generic.(cloud.StateBackendMetadataReader); ok {
		t.Fatal("generic provider unexpectedly acquired backend capabilities")
	}
	for _, bucket := range []bool{true, false} {
		t.Run(map[bool]string{true: "bucket", false: "table"}[bucket], func(t *testing.T) {
			p := &testutil.BackendProvider{TestProvider: &testutil.TestProvider{}}
			read := p.StateLockTableMetadata
			create := p.CreateStateLockTable
			reconcile := p.ReconcileStateLockTable
			component := &p.Table
			if bucket {
				read, reconcile, component = p.StateBucketMetadata, p.ReconcileStateBucket, &p.Bucket
				create = func(ctx context.Context, name string, desired map[string]string) (cloud.StateBackendCreateResult, error) {
					return p.CreateStateBucket(ctx, name, cloud.StateBucketCreateSpec{Region: "us-west-2", StateDocumentKey: state.StateDocumentKey, Tags: desired})
				}
			}
			ctx := context.Background()
			metadata, err := read(ctx, "chosen")
			if err != nil || metadata.Exists || len(metadata.Tags) != 0 {
				t.Fatalf("absence manufactured tags: %+v %v", metadata, err)
			}
			desired := (tags.Identity{}).Standard("setup", "test")
			result, err := create(ctx, "chosen", desired)
			if err != nil || !result.Created || result.Identifier != "chosen" {
				t.Fatalf("create: %+v %v", result, err)
			}
			desired[tags.ManagedByKey] = "changed input"
			metadata, err = read(ctx, "chosen")
			if err != nil || metadata.Tags[tags.ManagedByKey] != "fabrica" {
				t.Fatalf("create aliased tags: %+v %v", metadata, err)
			}
			metadata.Tags["Team"] = "changed output"
			if component.Tags["Team"] != "" {
				t.Fatal("metadata read aliases fake state")
			}
			before := maps.Clone(component.Tags)
			if result, err := create(ctx, "chosen", desired); err == nil || result.Created || !reflect.DeepEqual(before, component.Tags) {
				t.Fatal("create adopted an existing component")
			}
			spec := cloud.StateBackendReconcileSpec{Expected: *component, Tags: map[string]string{tags.ManagedByKey: "fabrica", "Team": "Tools"}}
			spec.Expected.Tags = maps.Clone(spec.Expected.Tags)
			component.Tags["Fresh"] = "preserve"
			if err := reconcile(ctx, "chosen", spec); err != nil || component.Tags["Team"] != "Tools" || component.Tags["Fresh"] != "preserve" {
				t.Fatalf("ordinary fresh tags lost: %v %v", component.Tags, err)
			}
			spec.Tags[tags.OwnershipVersionKey] = "1"
			if err := reconcile(ctx, "chosen", spec); err == nil {
				t.Fatal("reconcile installed a marker on an unmarked component")
			}
			delete(spec.Tags, tags.OwnershipVersionKey)
			for _, key := range []string{tags.ManagedByKey, tags.OwnershipVersionKey} {
				component.Tags[key] = "changed actual"
				if err := reconcile(ctx, "chosen", spec); err == nil {
					t.Fatalf("changed actual %s accepted", key)
				}
				component.Tags = maps.Clone(before)
			}
			component.Exists = false
			if err := reconcile(ctx, "chosen", spec); err == nil {
				t.Fatal("reconcile recreated a missing component")
			}
			component.Exists = true
			component.Tags[tags.OwnershipVersionKey] = "1"
			spec.Expected = *component
			spec.Expected.Tags = maps.Clone(component.Tags)
			if err := reconcile(ctx, "chosen", spec); err != nil || component.Tags[tags.OwnershipVersionKey] != "1" {
				t.Fatal("reconcile lost an existing marker")
			}
			if p.BackendCalls[0].Identifier != "chosen" || p.BackendCalls[0].Context != ctx {
				t.Fatal("fake lost actual target/context evidence")
			}
		})
	}
}

func TestBackendProviderDocumentsErrorsAndCancellation(t *testing.T) {
	p := &testutil.BackendProvider{TestProvider: &testutil.TestProvider{}}
	ctx := context.Background()
	if result, err := p.CreateStateBucket(ctx, "bucket", cloud.StateBucketCreateSpec{}); err == nil || result.Created || p.Bucket.Exists {
		t.Fatal("missing canonical key allowed a create")
	}
	if _, err := p.GetStateDocument(ctx, "bucket", "key"); !errors.Is(err, cloud.ErrStateDocumentMissing) {
		t.Fatal(err)
	}
	if err := p.PutStateDocument(ctx, "bucket", "key", []byte("document")); !errors.Is(err, cloud.ErrStateBackendAbsent) || len(p.StateDocuments) != 0 {
		t.Fatal("missing bucket accepted a canonical write")
	}
	p.Bucket.Exists = true
	if _, err := p.GetStateDocument(ctx, "bucket", "key"); !errors.Is(err, cloud.ErrStateDocumentMissing) {
		t.Fatal("empty canonical document was not reported missing")
	}
	if err := p.PutStateDocument(ctx, "bucket", "key", []byte("document")); err != nil {
		t.Fatal(err)
	}
	if doc, err := p.GetStateDocument(ctx, "bucket", "key"); err != nil || string(doc) != "document" {
		t.Fatalf("document round trip: %s %v", doc, err)
	}
	failure := errors.New("injected backend failure")
	p.StateStoreGetErr, p.StateStorePutErr = failure, failure
	if _, err := p.GetStateDocument(ctx, "bucket", "key"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := p.PutStateDocument(ctx, "bucket", "key", nil); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	p.BackendErr = map[string]error{"bucket metadata": failure}
	if _, err := p.StateBucketMetadata(ctx, "bucket"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	for _, call := range []func() error{
		func() error { _, err := p.StateBucketMetadata(cancelled, "bucket"); return err },
		func() error { _, err := p.StateLockTableMetadata(cancelled, "table"); return err },
		func() error {
			_, err := p.CreateStateBucket(cancelled, "bucket", cloud.StateBucketCreateSpec{StateDocumentKey: state.StateDocumentKey})
			return err
		},
		func() error { _, err := p.CreateStateLockTable(cancelled, "table", nil); return err },
		func() error { return p.ReconcileStateBucket(cancelled, "bucket", cloud.StateBackendReconcileSpec{}) },
		func() error { return p.ReconcileStateLockTable(cancelled, "table", cloud.StateBackendReconcileSpec{}) },
		func() error { _, err := p.GetStateDocument(cancelled, "bucket", "key"); return err },
		func() error { return p.PutStateDocument(cancelled, "bucket", "key", nil) },
	} {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Fatalf("operation ignored cancelled context: %v", err)
		}
	}
	if p.StateDocuments["bucket/key"] != "document" {
		t.Fatal("failed operation changed canonical state")
	}
}

func TestBackendLockingProviderRealAcquisitionAndConditionalRows(t *testing.T) {
	p := &testutil.BackendLockingProvider{BackendProvider: &testutil.BackendProvider{TestProvider: &testutil.TestProvider{AccountID: "account-one"}}}
	cfg := config.Defaults()
	cfg.State.Table = "chosen-table"
	rt := globals.Runtime{Config: cfg, Provider: p}
	ctx, cancelOperation := context.WithCancel(context.Background())
	defer cancelOperation()
	child, release, err := provision.AcquireStateLock(ctx, rt, "setup")
	if err != nil || child != ctx {
		t.Fatalf("missing table manufactured acquisition: %v", err)
	}
	release()
	p.Table.Exists = true
	child, release, err = provision.AcquireStateLock(ctx, rt, "setup")
	if err != nil || child == ctx {
		t.Fatalf("real acquisition did not return its child context: %v", err)
	}
	id := "fabrica-state/account-one"
	row := p.LockRow(cfg.State.Table, id)
	if row["Token"] == "" || row["Holder"] == "" || row["LockID"] != id {
		t.Fatalf("actual row missing identity: %v", row)
	}
	row["Holder"] = "mutated output"
	if p.LockRow(cfg.State.Table, id)["Holder"] == "mutated output" {
		t.Fatal("lock snapshot aliases stored row")
	}
	nested, nestedRelease, err := provision.AcquireStateLock(child, rt, "nested")
	if err != nil || nested != child {
		t.Fatal("nested actual context did not inherit its lock")
	}
	nestedRelease()
	if _, _, err := provision.AcquireStateLock(ctx, rt, "contender"); !errors.Is(err, provision.ErrStateLocked) {
		t.Fatalf("second holder succeeded: %v", err)
	}
	put := func(c context.Context, item map[string]string, condition string, values map[string]string) error {
		return p.AcquireStateLockRow(c, cfg.State.Table, item, condition, values)
	}
	store := state.NewFuncLockStore(cfg.State.Table, time.Minute, put, nil)
	if err := store.Renew(ctx, id, row["Token"], "renewed holder"); err != nil {
		t.Fatal(err)
	}
	if err := store.Renew(ctx, id, "wrong-token", "other"); !errors.Is(err, cloud.ErrLockHeld) {
		t.Fatalf("wrong-token renewal succeeded: %v", err)
	}
	if err := p.ReleaseStateLockRow(ctx, cfg.State.Table, id, "wrong-token"); !errors.Is(err, cloud.ErrLockHeld) {
		t.Fatalf("wrong-token release succeeded: %v", err)
	}
	if err := p.AcquireStateLockRow(ctx, cfg.State.Table, row, "unsupported", nil); err == nil {
		t.Fatal("unknown lock condition succeeded")
	}
	if err := p.AcquireStateLockRow(ctx, cfg.State.Table, row, "attribute_not_exists(LockID) OR AcquiredAt <= :stale", map[string]string{":stale": "invalid"}); err == nil {
		t.Fatal("malformed stale comparison succeeded")
	}
	cancelOperation()
	if !errors.Is(child.Err(), context.Canceled) {
		t.Fatal("actual acquired context ignored cancellation")
	}
	release()
	if len(p.LockRow(cfg.State.Table, id)) != 0 {
		t.Fatal("real release left the row")
	}
	ctx = context.Background()
	store.SetClock(func() time.Time { return time.Now().Add(-time.Hour) })
	if _, err := store.Acquire(ctx, id, "crashed holder"); err != nil {
		t.Fatal(err)
	}
	store.SetClock(time.Now)
	token, err := store.Acquire(ctx, id, "takeover")
	if err != nil || p.LockRow(cfg.State.Table, id)["Token"] != token {
		t.Fatalf("stale row could not be taken over: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := p.AcquireStateLockRow(cancelled, cfg.State.Table, row, state.RenewCondition, map[string]string{":token": token}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := p.ReleaseStateLockRow(cancelled, cfg.State.Table, id, token); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	p.Table.Exists = false
	if err := p.ReleaseStateLockRow(ctx, cfg.State.Table, id, token); !errors.Is(err, cloud.ErrLockTableMissing) {
		t.Fatal(err)
	}
}
