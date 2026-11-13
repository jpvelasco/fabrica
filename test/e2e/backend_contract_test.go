package e2e

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/tags"
)

func TestFakeBackendComponentContracts(t *testing.T) {
	p := &fakeProvider{store: newFakeStore()}
	for _, bucket := range []bool{true, false} {
		t.Run(map[bool]string{true: "bucket", false: "table"}[bucket], func(t *testing.T) {
			read, create, reconcile := p.StateLockTableMetadata, p.CreateStateLockTable, p.ReconcileStateLockTable
			existence, observed := p.store.tables, p.store.tableTags
			if bucket {
				read, reconcile = p.StateBucketMetadata, p.ReconcileStateBucket
				existence, observed = p.store.buckets, p.store.bucketTags
				create = func(ctx context.Context, name string, desired map[string]string) (cloud.StateBackendCreateResult, error) {
					return p.CreateStateBucket(ctx, name, cloud.StateBucketCreateSpec{StateDocumentKey: state.StateDocumentKey, Tags: desired})
				}
			}
			ctx := context.Background()
			existence["preexisting"] = true
			metadata, err := read(ctx, "preexisting")
			if err != nil || !metadata.Exists || len(metadata.Tags) != 0 {
				t.Fatal("boolean-only fixture manufactured ownership")
			}
			desired := tags.Standard("setup", "test")
			if result, err := create(ctx, "preexisting", desired); err == nil || result.Created || len(observed["preexisting"]) != 0 {
				t.Fatal("create adopted an untagged existing fixture")
			}
			if result, err := create(ctx, "new", desired); err != nil || !result.Created {
				t.Fatalf("fresh creation failed: %+v %v", result, err)
			}
			desired["Team"] = "changed caller"
			metadata, _ = read(ctx, "new")
			if metadata.Tags["Team"] != "" {
				t.Fatal("create retained caller map")
			}
			metadata.Tags["Team"] = "changed returned"
			if observed["new"]["Team"] != "" {
				t.Fatal("metadata aliases actual tags")
			}
			metadata, _ = read(ctx, "new")
			spec := cloud.StateBackendReconcileSpec{Expected: metadata, Tags: map[string]string{"Team": "Tools"}}
			observed["new"]["Fresh"] = "preserve"
			if err := reconcile(ctx, "new", spec); err != nil || observed["new"]["Fresh"] != "preserve" || observed["new"]["Team"] != "Tools" {
				t.Fatal("reconcile lost fresh ordinary tags")
			}
			before := maps.Clone(observed["new"])
			spec.Tags[tags.OwnershipVersionKey] = "1"
			if err := reconcile(ctx, "new", spec); err == nil || !reflect.DeepEqual(observed["new"], before) {
				t.Fatal("reconcile installed a marker")
			}
			delete(spec.Tags, tags.OwnershipVersionKey)
			observed["new"][tags.ManagedByKey] = "旧 actual owner"
			if err := reconcile(ctx, "new", spec); err == nil {
				t.Fatal("raw owner change accepted")
			}
			delete(observed["new"], tags.ManagedByKey)
			if err := reconcile(ctx, "new", spec); err == nil {
				t.Fatal("missing owner snapshot accepted")
			}
			observed["new"][tags.ManagedByKey] = before[tags.ManagedByKey]
			observed["new"][tags.OwnershipVersionKey] = "1"
			if err := reconcile(ctx, "new", spec); err == nil {
				t.Fatal("changed marker snapshot accepted")
			}
			metadata, _ = read(ctx, "new")
			spec.Expected = metadata
			if err := reconcile(ctx, "new", spec); err != nil || observed["new"][tags.OwnershipVersionKey] != "1" {
				t.Fatal("existing marker lost")
			}
			existence["new"] = false
			if err := reconcile(ctx, "new", spec); err == nil {
				t.Fatal("reconcile recreated a missing component")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, call := range []func() error{
		func() error { _, err := p.StateBucketMetadata(ctx, "bucket"); return err },
		func() error { _, err := p.StateLockTableMetadata(ctx, "table"); return err },
		func() error {
			_, err := p.CreateStateBucket(ctx, "bucket", cloud.StateBucketCreateSpec{StateDocumentKey: state.StateDocumentKey})
			return err
		},
		func() error { _, err := p.CreateStateLockTable(ctx, "table", nil); return err },
		func() error { return p.ReconcileStateBucket(ctx, "bucket", cloud.StateBackendReconcileSpec{}) },
		func() error { return p.ReconcileStateLockTable(ctx, "table", cloud.StateBackendReconcileSpec{}) },
		func() error { _, err := p.GetStateDocument(ctx, "bucket", "key"); return err },
		func() error { return p.PutStateDocument(ctx, "bucket", "key", nil) },
	} {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled fake operation succeeded: %v", err)
		}
	}
	if len(p.store.stateDocs) != 0 {
		t.Fatal("cancelled operations wrote canonical state")
	}
}
