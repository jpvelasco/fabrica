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
