package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/tags"
)

type inspectionMetadataProvider struct {
	plainProvider
	bucket, table       cloud.StateBackendMetadata
	bucketErr, tableErr error
	calls               []string
}

func (p *inspectionMetadataProvider) StateBucketMetadata(ctx context.Context, bucket string) (cloud.StateBackendMetadata, error) {
	p.calls = append(p.calls, "bucket:"+bucket)
	if err := ctx.Err(); err != nil {
		return cloud.StateBackendMetadata{}, err
	}
	return p.bucket, p.bucketErr
}
func (p *inspectionMetadataProvider) StateLockTableMetadata(_ context.Context, table string) (cloud.StateBackendMetadata, error) {
	p.calls = append(p.calls, "table:"+table)
	return p.table, p.tableErr
}

type inspectionProvider struct {
	inspectionMetadataProvider
	document []byte
	readErr  error
	writes   int
}

func (p *inspectionProvider) GetStateDocument(_ context.Context, bucket, key string) ([]byte, error) {
	p.calls = append(p.calls, "document:"+bucket+"/"+key)
	return p.document, p.readErr
}
func (p *inspectionProvider) PutStateDocument(context.Context, string, string, []byte) error {
	p.writes++
	return errors.New("unexpected state mutation")
}

func TestInspectBackendOwnershipDirectEvidenceWithoutWrites(t *testing.T) {
	for _, tt := range []struct {
		name, owner, document string
		metadata              cloud.StateBackendMetadata
		want                  BackendComponentAction
		wantErr               string
	}{
		{name: "absent default", want: BackendComponentCreate},
		{name: "default existing", metadata: backendMetadataForTest("fabrica"), want: BackendComponentReconcile},
		{name: "legacy empty repair", document: `{}`, metadata: backendMetadataForTest("old owner"), want: BackendComponentRepairLegacy},
		{name: "legacy inventory repair", document: `{"modules":[{"name":"horde"}],"history":[{"action":"create"}]}`, metadata: backendMetadataForTest("旧工具"), want: BackendComponentRepairLegacy},
		{name: "explicit default", document: `{"managedBy":"fabrica"}`, metadata: backendMetadataForTest("fabrica"), want: BackendComponentReconcile},
		{name: "custom bound", owner: "Studio-Tools", document: `{"managedBy":"Studio-Tools"}`, metadata: backendMetadataForTest("Studio-Tools"), want: BackendComponentReconcile},
		{name: "custom marked retry", owner: "Studio-Tools", metadata: backendMetadataForTest("Studio-Tools", "1"), want: BackendComponentReconcile},
		{name: "local inventory is not remote proof", metadata: backendMetadataForTest("Other"), wantErr: "ownership mismatch"},
		{name: "untagged", metadata: cloud.StateBackendMetadata{Exists: true}, wantErr: "ambiguous"},
		{name: "foreign canonical", document: `{"managedBy":"Other"}`, wantErr: "ownership mismatch"},
		{name: "wrong account", document: `{"account":"account-two"}`, wantErr: "target mismatch"},
		{name: "wrong region", document: `{"region":"us-east-1"}`, wantErr: "target mismatch"},
		{name: "null", document: `null`, wantErr: "expected a JSON object"},
		{name: "malformed", document: `{`, wantErr: "parsing state document"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			cache := filepath.Join(".fabrica", "state.json")
			if err := os.MkdirAll(filepath.Dir(cache), 0755); err != nil {
				t.Fatal(err)
			}
			local := `{"managedBy":"fabrica","modules":[{"name":"local-inventory"}]}`
			if err := os.WriteFile(cache, []byte(local), 0600); err != nil {
				t.Fatal(err)
			}
			p := &inspectionProvider{document: []byte(tt.document)}
			p.bucket, p.table = tt.metadata, tt.metadata
			if tt.document == "" {
				p.readErr = cloud.ErrStateDocumentMissing
			}
			identity, err := tags.ResolveIdentity(tt.owner)
			if err != nil {
				t.Fatal(err)
			}
			setup := NewSetupPlan(nil, "account-one", "us-west-2")
			evidence, plan, err := InspectBackendOwnership(context.Background(), p, setup, identity)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !reflect.DeepEqual(evidence, BackendOwnershipEvidence{}) || !reflect.DeepEqual(plan, BackendOwnershipPlan{}) {
					t.Fatalf("failed evidence returned authorization: evidence=%+v plan=%+v err=%v", evidence, plan, err)
				}
				if strings.Contains(err.Error(), "delete") || strings.Contains(err.Error(), "retag") {
					t.Fatalf("refusal suggested bypass: %v", err)
				}
			} else {
				if err != nil || plan.Bucket.Action != tt.want || plan.Table.Action != tt.want || (evidence.Canonical != nil) != (tt.document != "") {
					t.Fatalf("incorrect direct evidence decision: evidence=%+v plan=%+v err=%v", evidence, plan, err)
				}
				if tt.document != "" && len(evidence.Canonical.Modules) == 0 && strings.Contains(tt.document, "horde") {
					t.Fatal("canonical inventory was replaced")
				}
				if strings.Contains(tt.document, "history") && len(evidence.Canonical.History) != 1 {
					t.Fatal("canonical history was replaced")
				}
			}
			wantCalls := []string{"bucket:" + setup.Backend.Bucket, "table:" + setup.Backend.Table, "document:" + setup.Backend.Bucket + "/" + StateDocumentKey}
			if !reflect.DeepEqual(p.calls, wantCalls) || p.writes != 0 || string(p.document) != tt.document {
				t.Fatalf("inspection used another target or changed remote bytes: calls=%v writes=%d", p.calls, p.writes)
			}
			after, err := os.ReadFile(cache)
			if err != nil || string(after) != local {
				t.Fatalf("inspection refreshed the local cache: %v", err)
			}
		})
	}
}

func TestInspectBackendOwnershipAbsentDoesNotCreateCache(t *testing.T) {
	t.Chdir(t.TempDir())
	p := &inspectionProvider{readErr: cloud.ErrStateDocumentMissing}
	evidence, plan, err := InspectBackendOwnership(context.Background(), p, NewSetupPlan(nil, "account-one", "us-west-2"), tags.Identity{})
	if err != nil || evidence.Canonical != nil || plan.Bucket.Action != BackendComponentCreate || plan.Table.Action != BackendComponentCreate || p.writes != 0 {
		t.Fatalf("absent inspection manufactured canonical state: %+v %+v %v", evidence, plan, err)
	}
	if _, err := os.Stat(".fabrica"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only inspection created a cache directory: %v", err)
	}
}

func TestInspectBackendOwnershipFailsClosedBeforePartialEvidence(t *testing.T) {
	for _, tt := range []struct {
		name, want string
		provider   cloud.Provider
		setup      SetupPlan
		calls      int
	}{
		{name: "nil provider", want: "provider"},
		{name: "no metadata capability", provider: plainProvider{}, want: "metadata"},
		{name: "no canonical store", provider: &inspectionMetadataProvider{}, want: "state document"},
		{name: "empty bucket", provider: &inspectionProvider{}, setup: SetupPlan{Backend: BackendNames{Table: "table"}}, want: "backend names"},
		{name: "empty table", provider: &inspectionProvider{}, setup: SetupPlan{Backend: BackendNames{Bucket: "bucket"}}, want: "backend names"},
		{name: "bucket error", provider: &inspectionProvider{inspectionMetadataProvider: inspectionMetadataProvider{bucketErr: errors.New("access denied")}}, want: "bucket metadata", calls: 1},
		{name: "table error", provider: &inspectionProvider{inspectionMetadataProvider: inspectionMetadataProvider{tableErr: errors.New("access denied")}}, want: "table metadata", calls: 2},
		{name: "remote failure is not absence", provider: &inspectionProvider{readErr: errors.New("remote access denied")}, want: "remote access denied", calls: 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setup := tt.setup
			if setup.Backend == (BackendNames{}) {
				setup = NewSetupPlan(nil, "account-one", "us-west-2")
			}
			evidence, plan, err := InspectBackendOwnership(context.Background(), tt.provider, setup, tags.Identity{})
			if err == nil || !strings.Contains(err.Error(), tt.want) || !reflect.DeepEqual(evidence, BackendOwnershipEvidence{}) || !reflect.DeepEqual(plan, BackendOwnershipPlan{}) {
				t.Fatalf("read failure returned evidence: %+v %+v %v", evidence, plan, err)
			}
			if p, ok := tt.provider.(*inspectionProvider); ok && (len(p.calls) != tt.calls || p.writes != 0) {
				t.Fatalf("read failure performed later I/O: %v writes=%d", p.calls, p.writes)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &inspectionProvider{}
	_, _, err := InspectBackendOwnership(ctx, p, NewSetupPlan(nil, "account-one", "us-west-2"), tags.Identity{})
	if !errors.Is(err, context.Canceled) || len(p.calls) != 1 || p.writes != 0 {
		t.Fatalf("inspection lost cancellation or performed later I/O: %v %v", p.calls, err)
	}
}
