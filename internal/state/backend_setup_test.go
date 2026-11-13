package state

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/tags"
)

type setupApplyProvider struct {
	inspectionProvider
	steps             []string
	desired, expected map[string]map[string]string
	fail              map[string]error
	notCreated        string
	bucketSpec        cloud.StateBucketCreateSpec
	mutateArguments   bool
}

func (p *setupApplyProvider) record(ctx context.Context, phase string, desired map[string]string, expected cloud.StateBackendMetadata) error {
	p.steps = append(p.steps, phase)
	if err := ctx.Err(); err != nil {
		return err
	}
	p.desired[phase] = maps.Clone(desired)
	p.expected[phase] = maps.Clone(expected.Tags)
	if p.mutateArguments {
		clear(desired)
		clear(expected.Tags)
	}
	return p.fail[phase]
}
func (p *setupApplyProvider) CreateStateBucket(ctx context.Context, name string, spec cloud.StateBucketCreateSpec) (cloud.StateBackendCreateResult, error) {
	p.bucketSpec = spec
	err := p.record(ctx, "create bucket", spec.Tags, cloud.StateBackendMetadata{})
	return cloud.StateBackendCreateResult{Identifier: name, Created: p.notCreated != "bucket"}, err
}
func (p *setupApplyProvider) CreateStateLockTable(ctx context.Context, name string, desired map[string]string) (cloud.StateBackendCreateResult, error) {
	err := p.record(ctx, "create table", desired, cloud.StateBackendMetadata{})
	return cloud.StateBackendCreateResult{Identifier: name, Created: p.notCreated != "table"}, err
}
func (p *setupApplyProvider) ReconcileStateBucket(ctx context.Context, _ string, spec cloud.StateBackendReconcileSpec) error {
	return p.record(ctx, "reconcile bucket", spec.Tags, spec.Expected)
}
func (p *setupApplyProvider) ReconcileStateLockTable(ctx context.Context, _ string, spec cloud.StateBackendReconcileSpec) error {
	return p.record(ctx, "reconcile table", spec.Tags, spec.Expected)
}

func setupApplyFixture(t *testing.T, owner, document string, bucket, table cloud.StateBackendMetadata) (*setupApplyProvider, BackendSetupApplyPlan) {
	t.Helper()
	identity, err := tags.ResolveIdentity(owner)
	if err != nil {
		t.Fatal(err)
	}
	p := &setupApplyProvider{desired: make(map[string]map[string]string), expected: make(map[string]map[string]string)}
	p.bucket, p.table, p.document = bucket, table, []byte(document)
	if document == "" {
		p.readErr = cloud.ErrStateDocumentMissing
	}
	target := NewSetupPlan(nil, "account-one", "us-west-2")
	_, ownership, err := InspectBackendOwnership(context.Background(), p, target, identity)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanBackendSetup(target, identity, ownership, "v-test", map[string]string{"Team": "studio"})
	if err != nil {
		t.Fatal(err)
	}
	p.calls = nil
	return p, plan
}

func TestBackendSetupProtectedPreviewAndOwnedInputs(t *testing.T) {
	identity, _ := tags.ResolveIdentity("Studio-Tools")
	target := NewSetupPlan(nil, "account-one", "us-west-2")
	ownership := BackendOwnershipPlan{
		Bucket: BackendComponentDecision{Action: BackendComponentCreate},
		Table:  BackendComponentDecision{Action: BackendComponentReconcile, Observed: backendMetadataForTest("Studio-Tools")},
	}
	extra := map[string]string{"Team": "studio", "ManagedBy": "Other", "FabricaModule": "forged", "FabricaVersion": "forged", tags.OwnershipVersionKey: "2", "": "invalid", "FabricaCreateNonce": "forged"}
	plan, err := PlanBackendSetup(target, identity, ownership, "v-test", extra)
	if err != nil {
		t.Fatal(err)
	}
	target.Resources[0].Identifier = "changed"
	ownership.Table.Observed.Tags[tags.ManagedByKey] = "Other"
	extra["Team"] = "changed"
	want := identity.Standard("setup", "v-test")
	want["Team"] = "studio"
	if !reflect.DeepEqual(plan.TableTags, want) || plan.Target.Resources[0].Identifier != plan.Target.Backend.Bucket || plan.Ownership.Table.Observed.Tags[tags.ManagedByKey] != "Studio-Tools" {
		t.Fatalf("plan aliases input or installs an existing marker: %+v", plan)
	}
	want[tags.OwnershipVersionKey] = "1"
	if !reflect.DeepEqual(plan.BucketTags, want) {
		t.Fatalf("new custom tags = %v, want %v", plan.BucketTags, want)
	}
	plan.BucketTags["Team"] = "bucket-only"
	if plan.TableTags["Team"] != "studio" {
		t.Fatal("component tag maps alias each other")
	}
}

func TestApplyBackendSetupOrderedActionsAndExactTags(t *testing.T) {
	for _, tt := range []struct {
		name, owner, document string
		bucket, table         cloud.StateBackendMetadata
		want                  []string
	}{
		{name: "new default", want: []string{"create bucket", "create table"}},
		{name: "new custom", owner: "Studio-Tools", want: []string{"create bucket", "create table"}},
		{name: "existing default marked", bucket: backendMetadataForTest("fabrica", "1"), table: backendMetadataForTest("fabrica"), want: []string{"reconcile bucket", "reconcile table"}},
		{name: "custom canonical unmarked", owner: "Studio-Tools", document: `{"managedBy":"Studio-Tools","modules":[{"name":"horde"}],"history":[{"action":"create"}]}`, bucket: backendMetadataForTest("Studio-Tools"), table: backendMetadataForTest("Studio-Tools"), want: []string{"reconcile bucket", "reconcile table"}},
		{name: "custom marked retry and create", owner: "Studio-Tools", bucket: backendMetadataForTest("Studio-Tools", "1"), want: []string{"reconcile bucket", "create table"}},
		{name: "create then reconcile", table: backendMetadataForTest("fabrica"), want: []string{"create bucket", "reconcile table"}},
		{name: "raw legacy repair then create", document: `{}`, bucket: backendMetadataForTest("旧 owner"), want: []string{"reconcile bucket", "create table"}},
		{name: "reconcile then raw legacy repair", document: `{"managedBy":"fabrica"}`, bucket: backendMetadataForTest("fabrica"), table: backendMetadataForTest("old owner"), want: []string{"reconcile bucket", "reconcile table"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, plan := setupApplyFixture(t, tt.owner, tt.document, tt.bucket, tt.table)
			beforeBucket, beforeTable := maps.Clone(plan.BucketTags), maps.Clone(plan.TableTags)
			beforeObserved := maps.Clone(plan.Ownership.Bucket.Observed.Tags)
			// Fresh ordinary metadata may change without changing authorization.
			if p.bucket.Exists {
				p.bucket.Tags["FreshOnly"] = "keep"
			}
			p.mutateArguments = true
			results, err := ApplyBackendSetup(context.Background(), p, plan)
			if err != nil || len(results) != 2 || !reflect.DeepEqual(p.steps, tt.want) {
				t.Fatalf("ordered apply: results=%+v steps=%v err=%v", results, p.steps, err)
			}
			wantReads := []string{"bucket:" + plan.Target.Backend.Bucket, "table:" + plan.Target.Backend.Table, "document:" + plan.Target.Backend.Bucket + "/" + StateDocumentKey}
			if !reflect.DeepEqual(p.calls, wantReads) || !reflect.DeepEqual(p.desired[tt.want[0]], beforeBucket) || !reflect.DeepEqual(p.desired[tt.want[1]], beforeTable) {
				t.Fatalf("preview differs from actual writes or direct reads: %v %v", p.calls, p.desired)
			}
			if !reflect.DeepEqual(plan.BucketTags, beforeBucket) || !reflect.DeepEqual(plan.TableTags, beforeTable) || !reflect.DeepEqual(plan.Ownership.Bucket.Observed.Tags, beforeObserved) {
				t.Fatal("provider argument mutation changed the approved plan")
			}
			if p.bucket.Exists && p.expected[tt.want[0]]["FreshOnly"] != "keep" {
				t.Fatal("reconciler did not receive the fresh approved snapshot")
			}
			if tt.want[0] == "create bucket" && (p.bucketSpec.Region != plan.Target.Region || p.bucketSpec.StateDocumentKey != StateDocumentKey) {
				t.Fatalf("creator used wrong region/document key: %+v", p.bucketSpec)
			}
			for i, result := range results {
				name := "S3 bucket " + plan.Target.Backend.Bucket
				if i == 1 {
					name = "DynamoDB table " + plan.Target.Backend.Table
				}
				if result.Name != name || result.Existed != strings.HasPrefix(tt.want[i], "reconcile") {
					t.Fatalf("result lost identity/action: %+v", result)
				}
			}
			if string(p.document) != tt.document || p.writes != 0 {
				t.Fatal("component apply wrote canonical state or lost inventory")
			}
		})
	}
}

func TestBackendSetupRejectsInvalidOrMutatedPlans(t *testing.T) {
	for _, tt := range []struct {
		name string
		edit func(*BackendSetupApplyPlan)
	}{
		{name: "missing account", edit: func(p *BackendSetupApplyPlan) { p.Target.Account = "" }},
		{name: "missing region", edit: func(p *BackendSetupApplyPlan) { p.Target.Region = "" }},
		{name: "missing bucket", edit: func(p *BackendSetupApplyPlan) { p.Target.Backend.Bucket = "" }},
		{name: "missing table", edit: func(p *BackendSetupApplyPlan) { p.Target.Backend.Table = "" }},
		{name: "unknown bucket", edit: func(p *BackendSetupApplyPlan) { p.Ownership.Bucket.Action = BackendComponentUnknown }},
		{name: "unknown table", edit: func(p *BackendSetupApplyPlan) { p.Ownership.Table.Action = 255 }},
		{name: "create existing", edit: func(p *BackendSetupApplyPlan) { p.Ownership.Bucket.Observed.Exists = true }},
		{name: "create tagged absent", edit: func(p *BackendSetupApplyPlan) { p.Ownership.Table.Observed.Tags = map[string]string{"Team": "bad"} }},
		{name: "reconcile absent", edit: func(p *BackendSetupApplyPlan) { p.Ownership.Table.Action = BackendComponentReconcile }},
		{name: "repair absent", edit: func(p *BackendSetupApplyPlan) { p.Ownership.Bucket.Action = BackendComponentRepairLegacy }},
		{name: "owner tamper", edit: func(p *BackendSetupApplyPlan) { p.BucketTags[tags.ManagedByKey] = "Other" }},
		{name: "module tamper", edit: func(p *BackendSetupApplyPlan) { p.TableTags["FabricaModule"] = "Other" }},
		{name: "empty version", edit: func(p *BackendSetupApplyPlan) { p.TableTags["FabricaVersion"] = "" }},
		{name: "default marker install", edit: func(p *BackendSetupApplyPlan) { p.BucketTags[tags.OwnershipVersionKey] = "1" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, plan := setupApplyFixture(t, "", "", cloud.StateBackendMetadata{}, cloud.StateBackendMetadata{})
			tt.edit(&plan)
			results, err := ApplyBackendSetup(context.Background(), p, plan)
			if err == nil || len(results) != 0 || len(p.calls) != 0 || len(p.steps) != 0 {
				t.Fatalf("invalid plan performed I/O: results=%v reads=%v writes=%v err=%v", results, p.calls, p.steps, err)
			}
			if strings.HasPrefix(tt.name, "missing") || strings.HasPrefix(tt.name, "unknown") || strings.Contains(tt.name, "absent") || tt.name == "create existing" {
				got, err := PlanBackendSetup(plan.Target, plan.Identity, plan.Ownership, "v-test", nil)
				if err == nil || !reflect.DeepEqual(got, BackendSetupApplyPlan{}) {
					t.Fatalf("planning approved invalid shape: %+v %v", got, err)
				}
			}
		})
	}
	p, plan := setupApplyFixture(t, "Studio-Tools", `{"managedBy":"Studio-Tools"}`, backendMetadataForTest("Studio-Tools"), backendMetadataForTest("Studio-Tools", "1"))
	for _, edit := range []func(*BackendSetupApplyPlan){
		func(p *BackendSetupApplyPlan) { p.BucketTags[tags.OwnershipVersionKey] = "1" },
		func(p *BackendSetupApplyPlan) { delete(p.TableTags, tags.OwnershipVersionKey) },
		func(p *BackendSetupApplyPlan) { p.TableTags[tags.OwnershipVersionKey] = "2" },
	} {
		changed := plan
		changed.BucketTags, changed.TableTags = maps.Clone(plan.BucketTags), maps.Clone(plan.TableTags)
		edit(&changed)
		if _, err := ApplyBackendSetup(context.Background(), p, changed); err == nil || len(p.calls) != 0 || len(p.steps) != 0 {
			t.Fatalf("existing marker changed: %v reads=%v writes=%v", err, p.calls, p.steps)
		}
	}
	if got, err := PlanBackendSetup(plan.Target, plan.Identity, plan.Ownership, "", nil); err == nil || !reflect.DeepEqual(got, BackendSetupApplyPlan{}) {
		t.Fatalf("empty version planned: %+v %v", got, err)
	}
}

type setupCreatorOnly struct{ *inspectionProvider }

func (*setupCreatorOnly) CreateStateBucket(context.Context, string, cloud.StateBucketCreateSpec) (cloud.StateBackendCreateResult, error) {
	panic("capability check must precede writes")
}
func (*setupCreatorOnly) CreateStateLockTable(context.Context, string, map[string]string) (cloud.StateBackendCreateResult, error) {
	panic("capability check must precede writes")
}

type setupReconcilerOnly struct{ *inspectionProvider }

func (*setupReconcilerOnly) ReconcileStateBucket(context.Context, string, cloud.StateBackendReconcileSpec) error {
	panic("capability check must precede writes")
}
func (*setupReconcilerOnly) ReconcileStateLockTable(context.Context, string, cloud.StateBackendReconcileSpec) error {
	panic("capability check must precede writes")
}

func TestApplyBackendSetupChecksBothCapabilitiesBeforeWrites(t *testing.T) {
	p, plan := setupApplyFixture(t, "", "", cloud.StateBackendMetadata{}, backendMetadataForTest("fabrica"))
	for _, provider := range []cloud.Provider{nil, &setupCreatorOnly{&p.inspectionProvider}, &setupReconcilerOnly{&p.inspectionProvider}} {
		if result, err := ApplyBackendSetup(context.Background(), provider, plan); err == nil || len(result) != 0 || len(p.calls) != 0 {
			t.Fatalf("missing peer capability reached I/O: %v %v %v", result, err, p.calls)
		}
	}
}

func TestApplyBackendSetupFreshBothPeerBarrier(t *testing.T) {
	failure := errors.New("fresh read denied")
	for _, tt := range []struct {
		name string
		edit func(*setupApplyProvider)
		want error
	}{
		{name: "compatible table appeared", edit: func(p *setupApplyProvider) { p.table = backendMetadataForTest("fabrica") }},
		{name: "foreign table appeared", edit: func(p *setupApplyProvider) { p.table = backendMetadataForTest("Other") }},
		{name: "untagged table appeared", edit: func(p *setupApplyProvider) { p.table.Exists = true }},
		{name: "bucket disappeared", edit: func(p *setupApplyProvider) { p.bucket = cloud.StateBackendMetadata{} }},
		{name: "bucket owner changed", edit: func(p *setupApplyProvider) { p.bucket.Tags[tags.ManagedByKey] = "Other" }},
		{name: "marker appeared", edit: func(p *setupApplyProvider) { p.bucket.Tags[tags.OwnershipVersionKey] = "1" }},
		{name: "empty marker appeared", edit: func(p *setupApplyProvider) { p.bucket.Tags[tags.OwnershipVersionKey] = "" }},
		{name: "bucket read error", edit: func(p *setupApplyProvider) { p.bucketErr = failure }, want: failure},
		{name: "table read error", edit: func(p *setupApplyProvider) { p.tableErr = failure }, want: failure},
		{name: "canonical read error", edit: func(p *setupApplyProvider) { p.readErr = failure }, want: failure},
		{name: "foreign canonical", edit: func(p *setupApplyProvider) { p.readErr, p.document = nil, []byte(`{"managedBy":"Other"}`) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, plan := setupApplyFixture(t, "", "", backendMetadataForTest("fabrica"), cloud.StateBackendMetadata{})
			tt.edit(p)
			results, err := ApplyBackendSetup(context.Background(), p, plan)
			if err == nil || len(results) != 0 || len(p.steps) != 0 || tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("fresh peer failure allowed a bucket mutation: results=%v steps=%v err=%v", results, p.steps, err)
			}
		})
	}
	p, plan := setupApplyFixture(t, "", `{}`, backendMetadataForTest("old owner"), backendMetadataForTest("old owner"))
	p.bucket.Tags[tags.ManagedByKey] = "another old owner"
	if _, err := ApplyBackendSetup(context.Background(), p, plan); err == nil || len(p.steps) != 0 {
		t.Fatalf("same-action historical owner change ignored: %v %v", err, p.steps)
	}
	p.bucket.Tags[tags.ManagedByKey] = "old owner"
	p.readErr = cloud.ErrStateDocumentMissing
	if _, err := ApplyBackendSetup(context.Background(), p, plan); err == nil || len(p.steps) != 0 {
		t.Fatalf("missing canonical proof still repaired legacy tags: %v %v", err, p.steps)
	}
	p, plan = setupApplyFixture(t, "", "", cloud.StateBackendMetadata{}, cloud.StateBackendMetadata{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ApplyBackendSetup(ctx, p, plan); !errors.Is(err, context.Canceled) || len(p.steps) != 0 {
		t.Fatalf("cancelled fresh inspection reached writes: %v %v", err, p.steps)
	}
	p, plan = setupApplyFixture(t, "", "", backendMetadataForTest("fabrica", "1"), backendMetadataForTest("fabrica"))
	delete(p.bucket.Tags, tags.OwnershipVersionKey)
	if _, err := ApplyBackendSetup(context.Background(), p, plan); err == nil || len(p.steps) != 0 {
		t.Fatalf("same-action marker removal ignored: %v %v", err, p.steps)
	}
}

func TestApplyBackendSetupPhaseFailuresAndNoAdoption(t *testing.T) {
	failure := errors.New("component failed")
	for _, tt := range []struct {
		phase      string
		existing   bool
		notCreated bool
	}{
		{phase: "create bucket"}, {phase: "create table"},
		{phase: "reconcile bucket", existing: true}, {phase: "reconcile table", existing: true},
		{phase: "create bucket", notCreated: true}, {phase: "create table", notCreated: true},
	} {
		t.Run(tt.phase+map[bool]string{true: " false receipt"}[tt.notCreated], func(t *testing.T) {
			metadata := cloud.StateBackendMetadata{}
			if tt.existing {
				metadata = backendMetadataForTest("fabrica")
			}
			p, plan := setupApplyFixture(t, "", "", metadata, metadata)
			if tt.notCreated {
				p.notCreated = strings.TrimPrefix(tt.phase, "create ")
			} else {
				p.fail = map[string]error{tt.phase: failure}
			}
			results, err := ApplyBackendSetup(context.Background(), p, plan)
			if err == nil || !strings.Contains(err.Error(), strings.TrimPrefix(strings.TrimPrefix(tt.phase, "create "), "reconcile ")) || !tt.notCreated && !errors.Is(err, failure) {
				t.Fatalf("lost phase/cause: results=%v err=%v", results, err)
			}
			wantSteps := 1
			if strings.HasSuffix(tt.phase, "table") {
				wantSteps = 2
				if len(results) < 1 || results[0].Name != "S3 bucket "+plan.Target.Backend.Bucket {
					t.Fatalf("table failure lost handled bucket: %+v", results)
				}
			}
			if len(p.steps) != wantSteps || p.writes != 0 {
				t.Fatalf("phase failure continued or bound state: %v", p.steps)
			}
		})
	}
}
