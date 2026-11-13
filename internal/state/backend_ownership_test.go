package state

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/tags"
)

func backendMetadataForTest(owner string, marker ...string) cloud.StateBackendMetadata {
	metadata := cloud.StateBackendMetadata{Exists: true, Tags: map[string]string{
		tags.ManagedByKey: owner,
		"Team":            "Tools",
	}}
	if len(marker) != 0 {
		metadata.Tags[tags.OwnershipVersionKey] = marker[0]
	}
	return metadata
}

func TestClassifyBackendOwnershipMatrix(t *testing.T) {
	tests := []struct {
		name, owner, document string
		metadata              cloud.StateBackendMetadata
		want                  BackendComponentAction
		wantErr               string
	}{
		{name: "new default", want: BackendComponentCreate},
		{name: "existing default", metadata: backendMetadataForTest("fabrica"), want: BackendComponentReconcile},
		{name: "marked default", metadata: backendMetadataForTest("fabrica", "1"), want: BackendComponentReconcile},
		{name: "default foreign without proof", metadata: backendMetadataForTest("Other"), wantErr: "ownership mismatch"},
		{name: "default marked foreign", metadata: backendMetadataForTest("Other", "1"), wantErr: "ownership mismatch"},
		{name: "default historical text without proof", metadata: backendMetadataForTest("old owner"), wantErr: "ownership mismatch"},
		{name: "default nonASCII without proof", metadata: backendMetadataForTest("旧工具"), wantErr: "ownership mismatch"},
		{name: "new custom", owner: "Studio-Tools", want: BackendComponentCreate},
		{name: "marked custom retry", owner: "Studio-Tools", metadata: backendMetadataForTest("Studio-Tools", "1"), want: BackendComponentReconcile},
		{name: "unmarked custom ambiguous", owner: "Studio-Tools", metadata: backendMetadataForTest("Studio-Tools"), wantErr: "canonical"},
		{name: "custom cannot adopt default", owner: "Studio-Tools", metadata: backendMetadataForTest("fabrica"), wantErr: "ownership mismatch"},
		{name: "custom cannot adopt other owner", owner: "Studio-Tools", metadata: backendMetadataForTest("Other", "1"), wantErr: "ownership mismatch"},
		{name: "case differs", owner: "Studio-Tools", metadata: backendMetadataForTest("studio-tools", "1"), wantErr: "ownership mismatch"},
		{name: "legacy empty default", document: `{}`, metadata: backendMetadataForTest("fabrica"), want: BackendComponentReconcile},
		{name: "legacy empty inventory repair", document: `{"modules":[]}`, metadata: backendMetadataForTest("Other"), want: BackendComponentRepairLegacy},
		{name: "legacy populated repair", document: `{"modules":[{"name":"horde"}]}`, metadata: backendMetadataForTest("Other"), want: BackendComponentRepairLegacy},
		{name: "explicit default repair", document: `{"managedBy":"fabrica"}`, metadata: backendMetadataForTest("Other"), want: BackendComponentRepairLegacy},
		{name: "historical spaces repair", document: `{}`, metadata: backendMetadataForTest("old owner"), want: BackendComponentRepairLegacy},
		{name: "historical whitespace repair", document: `{}`, metadata: backendMetadataForTest(" "), want: BackendComponentRepairLegacy},
		{name: "historical nonASCII repair", document: `{}`, metadata: backendMetadataForTest("旧工具"), want: BackendComponentRepairLegacy},
		{name: "legacy cannot repair marked foreign", document: `{}`, metadata: backendMetadataForTest("Other", "1"), wantErr: "ownership mismatch"},
		{name: "legacy cannot repair marked malformed owner", document: `{}`, metadata: backendMetadataForTest("old owner", "1"), wantErr: "ownership mismatch"},
		{name: "legacy cannot repair marked nonASCII owner", document: `{}`, metadata: backendMetadataForTest("旧工具", "1"), wantErr: "ownership mismatch"},
		{name: "bound custom unmarked", owner: "Studio-Tools", document: `{"managedBy":"Studio-Tools"}`, metadata: backendMetadataForTest("Studio-Tools"), want: BackendComponentReconcile},
		{name: "bound custom marked", owner: "Studio-Tools", document: `{"managedBy":"Studio-Tools"}`, metadata: backendMetadataForTest("Studio-Tools", "1"), want: BackendComponentReconcile},
		{name: "bound custom missing peer", owner: "Studio-Tools", document: `{"managedBy":"Studio-Tools"}`, want: BackendComponentCreate},
		{name: "bound custom cannot repair foreign", owner: "Studio-Tools", document: `{"managedBy":"Studio-Tools"}`, metadata: backendMetadataForTest("Other"), wantErr: "ownership mismatch"},
		{name: "bound custom cannot repair malformed owner", owner: "Studio-Tools", document: `{"managedBy":"Studio-Tools"}`, metadata: backendMetadataForTest("old owner"), wantErr: "ownership mismatch"},
		{name: "default canonical blocks custom", owner: "Studio-Tools", document: `{}`, metadata: backendMetadataForTest("Studio-Tools", "1"), wantErr: "ownership mismatch"},
		{name: "custom canonical blocks default", document: `{"managedBy":"Studio-Tools"}`, metadata: backendMetadataForTest("fabrica"), wantErr: "ownership mismatch"},
		{name: "untagged", metadata: cloud.StateBackendMetadata{Exists: true}, wantErr: "ambiguous"},
		{name: "untagged despite canonical proof", document: `{}`, metadata: cloud.StateBackendMetadata{Exists: true}, wantErr: "ambiguous"},
		{name: "empty owner", metadata: backendMetadataForTest(""), wantErr: "ambiguous"},
		{name: "empty owner despite canonical proof", document: `{}`, metadata: backendMetadataForTest(""), wantErr: "ambiguous"},
		{name: "unknown marker", metadata: backendMetadataForTest("fabrica", "2"), wantErr: "ownership format"},
		{name: "empty marker", metadata: backendMetadataForTest("fabrica", ""), wantErr: "ownership format"},
		{name: "legacy unknown marker", document: `{}`, metadata: backendMetadataForTest("Other", "2"), wantErr: "ownership format"},
		{name: "legacy empty marker", document: `{}`, metadata: backendMetadataForTest("old owner", ""), wantErr: "ownership format"},
		{name: "custom unknown marker despite binding", owner: "Studio-Tools", document: `{"managedBy":"Studio-Tools"}`, metadata: backendMetadataForTest("Studio-Tools", "2"), wantErr: "ownership format"},
		{name: "absent component with tags", metadata: cloud.StateBackendMetadata{Tags: map[string]string{tags.ManagedByKey: "Other"}}, wantErr: "inconsistent"},
	}
	for _, tt := range tests {
		for _, component := range []string{"bucket", "table"} {
			t.Run(tt.name+"/"+component, func(t *testing.T) {
				identity, err := tags.ResolveIdentity(tt.owner)
				if err != nil {
					t.Fatal(err)
				}
				evidence := BackendOwnershipEvidence{}
				if tt.document != "" {
					evidence.Canonical, err = ParseState([]byte(tt.document), "", "")
					if err != nil {
						t.Fatal(err)
					}
				}
				before := string(EncodeState(evidence.Canonical))
				if component == "bucket" {
					evidence.Bucket = tt.metadata
				} else {
					evidence.Table = tt.metadata
				}
				plan, err := ClassifyBackendOwnership(identity, "", "", evidence)
				if tt.wantErr != "" {
					if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
						t.Fatalf("expected %q refusal, got %v", tt.wantErr, err)
					}
					if !reflect.DeepEqual(plan, BackendOwnershipPlan{}) {
						t.Fatalf("refusal returned partial authorization: %+v", plan)
					}
					if strings.Contains(err.Error(), "delete") || strings.Contains(err.Error(), "retag") {
						t.Fatalf("refusal suggested bypass: %v", err)
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					got, peer := plan.Bucket, plan.Table
					if component == "table" {
						got, peer = peer, got
					}
					if got.Action != tt.want || peer.Action != BackendComponentCreate {
						t.Fatalf("expected action %v and missing-peer creation, got %+v", tt.want, plan)
					}
					if !reflect.DeepEqual(got.Observed, tt.metadata) {
						t.Fatalf("snapshot dropped actual evidence: %+v", got.Observed)
					}
				}
				if string(EncodeState(evidence.Canonical)) != before {
					t.Fatal("classification changed canonical binding/inventory")
				}
			})
		}
	}
}

func TestClassifyBackendOwnershipCanonicalValidation(t *testing.T) {
	for _, tt := range []struct {
		name, document, account, region, changedOwner, wantErr string
		fresh                                                  bool
	}{
		{name: "fresh is not proof", fresh: true, wantErr: "unbound"},
		{name: "cleared binding", document: `{}`, changedOwner: "", wantErr: "binding changed"},
		{name: "changed binding", document: `{"managedBy":"Studio-Tools"}`, changedOwner: "fabrica", wantErr: "binding changed"},
		{name: "invalid binding", document: `{}`, changedOwner: "invalid owner", wantErr: "binding changed"},
		{name: "account changed", document: `{"account":"account-one"}`, account: "account-two", changedOwner: "fabrica", wantErr: "target mismatch"},
		{name: "region changed", document: `{"region":"us-west-2"}`, region: "us-east-1", changedOwner: "fabrica", wantErr: "target mismatch"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := NewState("", "")
			if !tt.fresh {
				var err error
				st, err = ParseState([]byte(tt.document), "", "")
				if err != nil {
					t.Fatal(err)
				}
				st.ManagedBy = tt.changedOwner
			}
			plan, err := ClassifyBackendOwnership(tags.Identity{}, tt.account, tt.region, BackendOwnershipEvidence{
				Canonical: st, Bucket: backendMetadataForTest("Other"), Table: backendMetadataForTest("Other"),
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !reflect.DeepEqual(plan, BackendOwnershipPlan{}) {
				t.Fatalf("invalid canonical proof authorized backend: plan=%+v err=%v", plan, err)
			}
			if strings.Contains(err.Error(), "delete") || strings.Contains(err.Error(), "retag") {
				t.Fatalf("canonical refusal suggested bypass: %v", err)
			}
			if tt.wantErr == "target mismatch" {
				for _, value := range []string{st.Account, st.Region, tt.account, tt.region, "restore"} {
					if !strings.Contains(err.Error(), value) {
						t.Fatalf("target refusal omitted %q: %v", value, err)
					}
				}
			}
		})
	}
}

func TestClassifyBackendOwnershipSnapshots(t *testing.T) {
	st, err := ParseState([]byte(`{"modules":[{"name":"horde"}],"history":[{"action":"create"}]}`), "", "")
	if err != nil {
		t.Fatal(err)
	}
	metadata := backendMetadataForTest("fabrica", "1")
	evidence := BackendOwnershipEvidence{Canonical: st, Bucket: metadata, Table: metadata}
	plan, err := ClassifyBackendOwnership(tags.Identity{}, "account-one", "us-east-1", evidence)
	if err != nil || plan.Bucket.Action != BackendComponentReconcile || plan.Table.Action != BackendComponentReconcile {
		t.Fatalf("compatible unknown target was refused: plan=%+v err=%v", plan, err)
	}
	metadata.Tags[tags.ManagedByKey] = "Other"
	metadata.Tags["Team"] = "Changed"
	for _, snapshot := range []cloud.StateBackendMetadata{plan.Bucket.Observed, plan.Table.Observed} {
		if snapshot.Tags[tags.ManagedByKey] != "fabrica" || snapshot.Tags["Team"] != "Tools" || snapshot.Tags[tags.OwnershipVersionKey] != "1" {
			t.Fatal("input mutation changed approved evidence")
		}
	}
	plan.Bucket.Observed.Tags["Team"] = "BucketOnly"
	if plan.Table.Observed.Tags["Team"] != "Tools" || metadata.Tags["Team"] != "Changed" {
		t.Fatal("snapshot maps share mutable ownership")
	}
	if BackendComponentUnknown == BackendComponentCreate {
		t.Fatal("zero action is not safe to return on failure")
	}
}

func TestClassifyBackendOwnershipRejectsPartialRepair(t *testing.T) {
	st, err := ParseState([]byte(`{}`), "", "")
	if err != nil {
		t.Fatal(err)
	}
	evidence := BackendOwnershipEvidence{
		Canonical: st,
		Bucket:    backendMetadataForTest("old owner"),
		Table:     backendMetadataForTest("Other", "1"),
	}
	for _, reverse := range []bool{false, true} {
		if reverse {
			evidence.Bucket, evidence.Table = evidence.Table, evidence.Bucket
		}
		plan, err := ClassifyBackendOwnership(tags.Identity{}, "", "", evidence)
		if err == nil || !reflect.DeepEqual(plan, BackendOwnershipPlan{}) {
			t.Fatalf("one repairable peer leaked authorization: plan=%+v err=%v", plan, err)
		}
	}
}
