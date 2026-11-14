package provision

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/tags"
)

func TestCustomMutationRequiresCompatibleBackend(t *testing.T) {
	for _, mode := range []string{"compatible", "no locking", "no storage", "invalid", "foreign", "absent", "table absent", "unmarked", "remote mismatch"} {
		t.Run(mode, func(t *testing.T) {
			rt, p := leasePersistenceRuntime(t)
			rt.Config.Cloud.AWS.Identity.ManagedBy = "Studio-Tools"
			metadata := cloud.StateBackendMetadata{Exists: true, Tags: map[string]string{tags.ManagedByKey: "Studio-Tools", tags.OwnershipVersionKey: "1"}}
			p.Bucket, p.Table = metadata, metadata
			want := ""
			switch mode {
			case "no locking":
				rt.Provider = p.TestProvider
				want = "state locking"
			case "no storage":
				rt.Provider = struct {
					cloud.Provider
					cloud.StateLockManager
					cloud.StateBackendMetadataReader
				}{p, p, p}
				want = "state document"
			case "invalid":
				rt.Config.Cloud.AWS.Identity.ManagedBy = "invalid owner"
				want = "managedBy"
			case "foreign":
				p.Bucket = cloud.StateBackendMetadata{Exists: true, Tags: map[string]string{tags.ManagedByKey: "other"}}
				want = "ownership mismatch"
			case "absent":
				p.Bucket = cloud.StateBackendMetadata{}
				want = "run 'fabrica setup'"
			case "table absent":
				p.Table = cloud.StateBackendMetadata{}
				want = "run 'fabrica setup'"
			case "unmarked":
				p.Bucket = cloud.StateBackendMetadata{Exists: true, Tags: map[string]string{tags.ManagedByKey: "Studio-Tools"}}
				want = "ownership format marker"
			case "remote mismatch":
				p.StateDocuments = map[string]string{stateTestKey(): `{"managedBy":"other"}`}
				want = "ownership mismatch"
			}
			ctx, release, err := AcquireStateLock(t.Context(), rt, "custom create")
			defer release()
			if want != "" {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("guard = %v, want %q", err, want)
				}
				return
			}
			if err != nil || !StateLockHeld(ctx) {
				t.Fatalf("compatible backend refused: %v", err)
			}
			st := state.NewState(rt.Config.Cloud.AWS.AccountID, rt.Config.Cloud.AWS.Region)
			if err := StateWriter(ctx, rt)(st); err != nil || st.ManagedBy != "Studio-Tools" {
				t.Fatalf("custom writer = %v, owner %q", err, st.ManagedBy)
			}
		})
	}
}
