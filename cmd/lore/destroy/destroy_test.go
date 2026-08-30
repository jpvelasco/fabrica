package destroy

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// ---- Spec validation ----

func TestSpecFields(t *testing.T) {
	if spec.ModuleName != "lore" {
		t.Errorf("ModuleName = %q, want lore", spec.ModuleName)
	}
	if spec.Verb != "destroy" {
		t.Errorf("Verb = %q, want destroy", spec.Verb)
	}
	if spec.VersionLabel != "AMI ID" {
		t.Errorf("VersionLabel = %q", spec.VersionLabel)
	}
	if spec.Title != "Lore loreserver" {
		t.Errorf("Title = %q", spec.Title)
	}
	if spec.NotProvisioned == "" {
		t.Error("NotProvisioned should not be empty")
	}
	if spec.PlanHeader == "" {
		t.Error("PlanHeader should not be empty")
	}
	if spec.DryRunHeader == "" {
		t.Error("DryRunHeader should not be empty")
	}
	if spec.Irreversible == "" {
		t.Error("Irreversible should not be empty")
	}
	if spec.SuccessMessage == "" {
		t.Error("SuccessMessage should not be empty")
	}
}

// ---- NewTeardown ----

func TestNewTeardownNilProvider(t *testing.T) {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: nil}
	tc := NewTeardown(rt, io.Discard)

	if tc.Spec.ModuleName != "lore" {
		t.Errorf("ModuleName = %q, want lore", tc.Spec.ModuleName)
	}
	if !tc.SkipConfirm {
		t.Error("SkipConfirm should be true for orchestrated teardown")
	}
	if !tc.AssumeYes {
		t.Error("AssumeYes should be true for orchestrated teardown")
	}
	if tc.ReadState == nil {
		t.Error("ReadState must be wired")
	}
	if tc.WriteState == nil {
		t.Error("WriteState must be wired")
	}
	if tc.Confirm == nil {
		t.Error("Confirm must be wired")
	}
	if tc.DeleteResource != nil {
		t.Error("DeleteResource must be nil when provider is nil")
	}
	if tc.GetResource != nil {
		t.Error("GetResource must be nil when provider is nil")
	}
}

func TestNewTeardownWithProvider(t *testing.T) {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: &testutil.TestProvider{}}
	tc := NewTeardown(rt, io.Discard)

	if tc.Spec.ModuleName != "lore" {
		t.Errorf("ModuleName = %q, want lore", tc.Spec.ModuleName)
	}
	if !tc.SkipConfirm {
		t.Error("SkipConfirm should be true")
	}
	if !tc.AssumeYes {
		t.Error("AssumeYes should be true")
	}
	if tc.DeleteResource == nil {
		t.Error("DeleteResource must be wired when provider is set")
	}
	if tc.GetResource == nil {
		t.Error("GetResource must be wired when provider is set")
	}
}

func TestNewTeardownWithProviderNilRC(t *testing.T) {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: &testutil.NilResourceProvider{}}
	tc := NewTeardown(rt, io.Discard)

	if tc.DeleteResource != nil {
		t.Error("DeleteResource must be nil when Resources() returns nil")
	}
	if tc.GetResource != nil {
		t.Error("GetResource must be nil when Resources() returns nil")
	}
}

// ---- New command ----

func TestNewCommandUse(t *testing.T) {
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults()}, nil
	}
	opts := func() globals.Options { return globals.Options{} }
	cmd := New(rt, opts, io.Discard)

	if cmd.Use != "destroy" {
		t.Errorf("Use = %q, want destroy", cmd.Use)
	}
	if cmd.Short != "Permanently delete the Lore server" {
		t.Errorf("Short = %q", cmd.Short)
	}
	if cmd.Long == "" {
		t.Error("Long should not be empty")
	}
}

func TestNewCommandWithProvider(t *testing.T) {
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults(), Provider: &testutil.TestProvider{}}, nil
	}
	opts := func() globals.Options { return globals.Options{} }
	cmd := New(rt, opts, io.Discard)

	if cmd.RunE == nil {
		t.Error("RunE must be set")
	}
}

// ---- ResourceOrder tests ----

func TestLoreResourceOrder_S3Enabled(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-lore123"},
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-lore123"},
			{TypeName: "AWS::S3::Bucket", Identifier: "fabrica-lore-store-12345"},
			{TypeName: "AWS::IAM::Role", Identifier: "fabrica-lore-s3-role"},
			{TypeName: "AWS::IAM::InstanceProfile", Identifier: "fabrica-lore-s3-profile"},
			{TypeName: "AWS::DynamoDB::Table", Identifier: "fabrica-lore-store-12345-fragments"},
			{TypeName: "AWS::DynamoDB::Table", Identifier: "fabrica-lore-store-12345-metadata"},
			{TypeName: "AWS::DynamoDB::Table", Identifier: "fabrica-lore-store-12345-mutable"},
			{TypeName: "AWS::DynamoDB::Table", Identifier: "fabrica-lore-store-12345-locks"},
		},
	}

	resources := loreResourceOrder(m)
	if len(resources) != 9 {
		t.Fatalf("loreResourceOrder returned %d resources, want 9", len(resources))
	}

	// Build index of each resource by identifier for order assertions.
	byID := make(map[string]int)
	for i, r := range resources {
		byID[r.Identifier] = i
	}

	// Instance must be first (terminated before dependent resources).
	if idx, ok := byID["i-lore123"]; !ok {
		t.Error("Instance not found in destroy order")
	} else if idx != 0 {
		t.Errorf("Instance at index %d, want 0 (first)", idx)
	}

	// Instance Profile must come before Role.
	profileIdx, hasProfile := byID["fabrica-lore-s3-profile"]
	roIdx, hasRole := byID["fabrica-lore-s3-role"]
	if !hasProfile {
		t.Error("InstanceProfile not found")
	}
	if !hasRole {
		t.Error("Role not found")
	}
	if hasProfile && hasRole && profileIdx >= roIdx {
		t.Errorf("InstanceProfile (idx %d) must be deleted before Role (idx %d)", profileIdx, roIdx)
	}

	// Tables must be deleted after the instance and role, before the bucket:
	// the store is live-writable while the instance runs, and the tables are
	// provable empty only once the instance is gone.
	bucketIdx, hasBucket := byID["fabrica-lore-store-12345"]
	if !hasBucket {
		t.Error("S3 Bucket not found")
	}
	for _, table := range []string{
		"fabrica-lore-store-12345-fragments",
		"fabrica-lore-store-12345-metadata",
		"fabrica-lore-store-12345-mutable",
		"fabrica-lore-store-12345-locks",
	} {
		tblIdx, hasTable := byID[table]
		if !hasTable {
			t.Errorf("table %s not found in destroy order", table)
			continue
		}
		if hasRole && tblIdx <= roIdx {
			t.Errorf("table %s (idx %d) must be deleted after the IAM role (idx %d)", table, tblIdx, roIdx)
		}
		if tblIdx >= bucketIdx {
			t.Errorf("table %s (idx %d) must be deleted before the S3 bucket (idx %d)", table, tblIdx, bucketIdx)
		}
	}

	// S3 Bucket must come before Security Group, which is last.
	sgIdx, hasSG := byID["sg-lore123"]
	if !hasSG {
		t.Error("SecurityGroup not found")
	} else if bucketIdx >= sgIdx {
		t.Errorf("S3 Bucket (idx %d) must be deleted before SecurityGroup (idx %d)", bucketIdx, sgIdx)
	}
	if sgIdx != 8 {
		t.Errorf("SecurityGroup at index %d, want 8 (last)", sgIdx)
	}
}

func TestLoreResourceOrder_S3Disabled(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-lore123"},
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-lore123"},
		},
	}

	resources := loreResourceOrder(m)
	if len(resources) != 2 {
		t.Fatalf("loreResourceOrder returned %d resources, want 2", len(resources))
	}

	if resources[0].TypeName != cloud.TypeAWSEC2Instance {
		t.Errorf("first resource = %s, want AWS::EC2::Instance", resources[0].TypeName)
	}
	if resources[0].Identifier != "i-lore123" {
		t.Errorf("first resource ID = %s, want i-lore123", resources[0].Identifier)
	}

	if resources[1].TypeName != cloud.TypeAWSEC2SecurityGroup {
		t.Errorf("second resource = %s, want AWS::EC2::SecurityGroup", resources[1].TypeName)
	}
	if resources[1].Identifier != "sg-lore123" {
		t.Errorf("second resource ID = %s, want sg-lore123", resources[1].Identifier)
	}
}

func TestLoreResourceOrder_EmptyModule(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{},
	}

	resources := loreResourceOrder(m)
	if len(resources) != 0 {
		t.Errorf("loreResourceOrder returned %d resources, want 0", len(resources))
	}
}

func TestLoreResourceOrder_SkipsEmptyIdentifiers(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: ""},
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-lore123"},
		},
	}

	resources := loreResourceOrder(m)
	if len(resources) != 1 {
		t.Errorf("got %d resources, want 1 (empty identifier skipped)", len(resources))
	}
	if len(resources) > 0 && resources[0].Identifier != "sg-lore123" {
		t.Errorf("resource ID = %s, want sg-lore123", resources[0].Identifier)
	}
}

func TestLoreResourceOrder_TableOnlyLeftover(t *testing.T) {
	// Orphan recovery: only the four DynamoDB tables remain (instance/role/bucket/SG already gone).
	// Destroy must still delete the tables even without the instance or bucket.
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::DynamoDB::Table", Identifier: "fabrica-lore-store-123-fragments"},
			{TypeName: "AWS::DynamoDB::Table", Identifier: "fabrica-lore-store-123-metadata"},
			{TypeName: "AWS::DynamoDB::Table", Identifier: "fabrica-lore-store-123-mutable"},
			{TypeName: "AWS::DynamoDB::Table", Identifier: "fabrica-lore-store-123-locks"},
		},
	}

	resources := loreResourceOrder(m)
	if len(resources) != 4 {
		t.Fatalf("loreResourceOrder table-only returned %d, want 4", len(resources))
	}
	for i, r := range resources {
		if r.TypeName != cloud.TypeAWSDynamoDBTable {
			t.Errorf("[%d] TypeName = %q, want %q", i, r.TypeName, cloud.TypeAWSDynamoDBTable)
		}
	}
	// Ensure no phantom bucket or SG is injected.
	for _, r := range resources {
		if r.TypeName == cloud.TypeAWSS3Bucket || r.TypeName == cloud.TypeAWSEC2SecurityGroup {
			t.Errorf("table-only state must not include %s", r.TypeName)
		}
	}
}

func TestLoreResourceOrder_StatusAgnostic(t *testing.T) {
	// Tables must be deleted regardless of module status (provisioning vs ready).
	// A previous bug gated table deletion on status == "ready".
	for _, status := range []string{"provisioning", "ready", "destroying"} {
		t.Run(status, func(t *testing.T) {
			m := &fabricastate.ModuleState{
				Status: status,
				Resources: []fabricastate.ModuleResource{
					{TypeName: "AWS::EC2::Instance", Identifier: "i-1"},
					{TypeName: "AWS::DynamoDB::Table", Identifier: "fabrica-lore-store-123-fragments"},
					{TypeName: "AWS::S3::Bucket", Identifier: "fabrica-lore-store-123"},
					{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-1"},
				},
			}
			resources := loreResourceOrder(m)
			found := false
			for _, r := range resources {
				if r.Identifier == "fabrica-lore-store-123-fragments" {
					found = true
				}
			}
			if !found {
				t.Errorf("status %q: table not in destroy plan", status)
			}
		})
	}
}

func TestLoreDestroyS3Store_DryRunContainsTables(t *testing.T) {
	// Full S3-store state: dry-run output must list all four tables before the bucket.
	m := &fabricastate.ModuleState{
		Name:    "lore",
		Version: "ami-0abc123",
		Status:  "ready",
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-lore123"},
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-lore123"},
			{TypeName: "AWS::S3::Bucket", Identifier: "fabrica-lore-store-123"},
			{TypeName: "AWS::IAM::Role", Identifier: "fabrica-lore-role"},
			{TypeName: "AWS::IAM::InstanceProfile", Identifier: "fabrica-lore-profile"},
			{TypeName: "AWS::DynamoDB::Table", Identifier: "fabrica-lore-store-123-fragments"},
			{TypeName: "AWS::DynamoDB::Table", Identifier: "fabrica-lore-store-123-metadata"},
			{TypeName: "AWS::DynamoDB::Table", Identifier: "fabrica-lore-store-123-mutable"},
			{TypeName: "AWS::DynamoDB::Table", Identifier: "fabrica-lore-store-123-locks"},
		},
	}
	resources := loreResourceOrder(m)
	if len(resources) != 9 {
		t.Fatalf("want 9 resources, got %d", len(resources))
	}
	// Verify order: instance → profile → role → tables → bucket → SG
	byID := make(map[string]int)
	for i, r := range resources {
		byID[r.Identifier] = i
	}
	bucketIdx := byID["fabrica-lore-store-123"]
	for _, tbl := range []string{
		"fabrica-lore-store-123-fragments",
		"fabrica-lore-store-123-metadata",
		"fabrica-lore-store-123-mutable",
		"fabrica-lore-store-123-locks",
	} {
		idx, ok := byID[tbl]
		if !ok {
			t.Fatalf("table %s missing from plan", tbl)
		}
		if idx >= bucketIdx {
			t.Errorf("table %s (idx %d) must be before bucket (idx %d)", tbl, idx, bucketIdx)
		}
	}
}

func TestLoreDestroyS3Store_LocalStoreHasNoTableDeletes(t *testing.T) {
	// Local store has no tables in state; plan must not invent any.
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-lore123"},
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-lore123"},
		},
	}
	resources := loreResourceOrder(m)
	for _, r := range resources {
		if r.TypeName == cloud.TypeAWSDynamoDBTable {
			t.Errorf("local-store plan must not contain DynamoDB table %s", r.Identifier)
		}
	}
	if len(resources) != 2 {
		t.Errorf("local-store plan len = %d, want 2", len(resources))
	}
}

func TestLoreDestroyS3Store_AfterSuccessfulDestroy_NoTablesRemain(t *testing.T) {
	// Simulate a successful destroy: after deleting all 9, state must be empty.
	// This catches the orphan bug where tables were dropped from the plan and
	// then forgotten via RemoveModule.
	m := &fabricastate.ModuleState{
		Name:    "lore",
		Version: "ami-0abc123",
		Status:  "ready",
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-lore123"},
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-lore123"},
			{TypeName: "AWS::S3::Bucket", Identifier: "fabrica-lore-store-123"},
			{TypeName: "AWS::IAM::Role", Identifier: "fabrica-lore-role"},
			{TypeName: "AWS::IAM::InstanceProfile", Identifier: "fabrica-lore-profile"},
			{TypeName: "AWS::DynamoDB::Table", Identifier: "fabrica-lore-store-123-fragments"},
			{TypeName: "AWS::DynamoDB::Table", Identifier: "fabrica-lore-store-123-metadata"},
			{TypeName: "AWS::DynamoDB::Table", Identifier: "fabrica-lore-store-123-mutable"},
			{TypeName: "AWS::DynamoDB::Table", Identifier: "fabrica-lore-store-123-locks"},
		},
	}
	resources := loreResourceOrder(m)
	// Simulate delete loop: remove each resource from m.Resources
	for _, r := range resources {
		filtered := m.Resources[:0]
		for _, existing := range m.Resources {
			if existing.TypeName == r.TypeName && existing.Identifier == r.Identifier {
				continue
			}
			filtered = append(filtered, existing)
		}
		m.Resources = filtered
	}
	if len(m.Resources) != 0 {
		t.Errorf("after deleting all planned resources, %d resources remain: %+v", len(m.Resources), m.Resources)
	}
	for _, r := range m.Resources {
		if r.TypeName == cloud.TypeAWSDynamoDBTable {
			t.Errorf("table %s survived destroy", r.Identifier)
		}
	}
}

// fakeBucketCleaner records purge calls for the SDKDeleteFunc wiring test.
type fakeBucketCleaner struct {
	purged []string
}

func (f *fakeBucketCleaner) PurgeBucket(_ context.Context, bucket string) error {
	f.purged = append(f.purged, bucket)
	return nil
}

// TestWireSDKDeletePurgesStoreBucket verifies the Lore teardown purges the
// versioned store bucket via the S3BucketCleaner auxiliary interface, then
// hands the empty bucket back to Cloud Control deletion (ErrNotHandled).
func TestWireSDKDeletePurgesStoreBucket(t *testing.T) {
	cleaner := &fakeBucketCleaner{}
	provider := struct {
		*testutil.TestProvider
		*fakeBucketCleaner
	}{TestProvider: &testutil.TestProvider{}, fakeBucketCleaner: cleaner}
	tc := NewTeardown(globals.Runtime{Config: &config.Config{}, Provider: provider}, io.Discard)
	if tc.SDKDeleteFunc == nil {
		t.Fatal("SDKDeleteFunc not wired; store bucket would never be emptied")
	}

	err := tc.SDKDeleteFunc(context.Background(), "AWS::S3::Bucket", "lore-store-bucket")
	if !errors.Is(err, cloud.ErrNotHandled) {
		t.Fatalf("after purging, hook must fall through to Cloud Control: %v", err)
	}
	if len(cleaner.purged) != 1 || cleaner.purged[0] != "lore-store-bucket" {
		t.Errorf("purged = %v, want [lore-store-bucket]", cleaner.purged)
	}

	// Non-bucket resources are not handled by this hook.
	err = tc.SDKDeleteFunc(context.Background(), "AWS::EC2::Instance", "i-1")
	if !errors.Is(err, cloud.ErrNotHandled) {
		t.Errorf("non-bucket type = %v, want ErrNotHandled", err)
	}
	if len(cleaner.purged) != 1 {
		t.Errorf("purge called for non-bucket resource: %v", cleaner.purged)
	}
}

// TestTeardownS3Store_Integration verifies the full teardown engine deletes
// all 9 S3-store resources in documented order when using the real spec.
// This is the regression test for the orphan bug where tables were dropped
// from the plan (5 vs 9) and then forgotten via RemoveModule.
func TestTeardownS3Store_Integration(t *testing.T) {
	// Build a state with the full S3 store (instance + profile + role + 4 tables + bucket + SG).
	st := fabricastate.NewState("123456789012", "us-east-1")
	bucket := "fabrica-lore-store-123456789012-us-east-1"
	resources := []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-lore123"},
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-lore123"},
		{TypeName: cloud.TypeAWSS3Bucket, Identifier: bucket},
		{TypeName: cloud.TypeAWSIAMRole, Identifier: "fabrica-lore-role"},
		{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "fabrica-lore-profile"},
		{TypeName: cloud.TypeAWSDynamoDBTable, Identifier: bucket + "-fragments"},
		{TypeName: cloud.TypeAWSDynamoDBTable, Identifier: bucket + "-metadata"},
		{TypeName: cloud.TypeAWSDynamoDBTable, Identifier: bucket + "-mutable"},
		{TypeName: cloud.TypeAWSDynamoDBTable, Identifier: bucket + "-locks"},
	}
	st.UpsertModule("lore", "ami-0abc123", "ready", resources)

	// Track delete order explicitly.
	var deleted []string
	provider := &testutil.TestProvider{}
	// Wrap the provider's Resources to capture order.
	origResources := provider.Resources()
	capturingProvider := &capturingProvider{
		TestProvider: provider,
		captured:     &deleted,
		origRC:       origResources,
	}
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: capturingProvider}
	tc := NewTeardown(rt, io.Discard)
	tc.ReadState = func() (*fabricastate.State, error) { return st, nil }
	var lastWritten *fabricastate.State
	tc.WriteState = func(s *fabricastate.State) error {
		cp := *s
		lastWritten = &cp
		return nil
	}

	// Run the teardown (orchestrated path: SkipConfirm + AssumeYes).
	if err := tc.Run(context.Background()); err != nil {
		t.Fatalf("teardown Run: %v", err)
	}

	if len(deleted) != 9 {
		t.Fatalf("expected 9 delete calls (instance+profile+role+4 tables+bucket+SG), got %d: %v", len(deleted), deleted)
	}

	// Verify order: instance → profile → role → tables → bucket → SG
	byID := make(map[string]int)
	for i, id := range deleted {
		byID[id] = i
	}
	bucketIdx, ok := byID[bucket]
	if !ok {
		t.Fatal("bucket not found in delete order")
	}
	for _, suffix := range []string{"-fragments", "-metadata", "-mutable", "-locks"} {
		id := bucket + suffix
		idx, ok := byID[id]
		if !ok {
			t.Fatalf("table %s missing from delete order", id)
		}
		if idx >= bucketIdx {
			t.Errorf("table %s (idx %d) must be before bucket (idx %d)", id, idx, bucketIdx)
		}
		// Also verify table after role.
		roleIdx, hasRole := byID["fabrica-lore-role"]
		if hasRole && idx <= roleIdx {
			t.Errorf("table %s (idx %d) must be after role (idx %d)", id, idx, roleIdx)
		}
	}
	// Instance must be first, SG last.
	if deleted[0] != "i-lore123" {
		t.Errorf("first deleted = %q, want i-lore123", deleted[0])
	}
	if deleted[len(deleted)-1] != "sg-lore123" {
		t.Errorf("last deleted = %q, want sg-lore123", deleted[len(deleted)-1])
	}

	// After successful destroy, state must have no lore module and no tables.
	if lastWritten == nil {
		t.Fatal("state was never written")
	}
	if m := lastWritten.GetModule("lore"); m != nil {
		t.Errorf("lore module must be removed from state after destroy, still has %d resources", len(m.Resources))
		for _, r := range m.Resources {
			if r.TypeName == cloud.TypeAWSDynamoDBTable {
				t.Errorf("orphaned table %s still in state", r.Identifier)
			}
		}
	}
}

// capturingProvider wraps TestProvider to capture delete order.
type capturingProvider struct {
	*testutil.TestProvider
	captured *[]string
	origRC   cloud.ResourceClient
}

func (c *capturingProvider) Resources() cloud.ResourceClient {
	return &capturingClient{orig: c.origRC, captured: c.captured, provider: c.TestProvider}
}

type capturingClient struct {
	orig     cloud.ResourceClient
	captured *[]string
	provider *testutil.TestProvider
}

func (cc *capturingClient) Create(ctx context.Context, r *cloud.Resource) error {
	return cc.orig.Create(ctx, r)
}
func (cc *capturingClient) Get(ctx context.Context, r *cloud.Resource) error {
	return cc.orig.Get(ctx, r)
}
func (cc *capturingClient) Update(ctx context.Context, r *cloud.Resource) error {
	return cc.orig.Update(ctx, r)
}
func (cc *capturingClient) Delete(ctx context.Context, r *cloud.Resource) error {
	*cc.captured = append(*cc.captured, r.Identifier)
	return cc.orig.Delete(ctx, r)
}
func (cc *capturingClient) List(ctx context.Context, typeName string) ([]cloud.Resource, error) {
	return cc.orig.List(ctx, typeName)
}
