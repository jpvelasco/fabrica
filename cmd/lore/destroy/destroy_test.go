package destroy

import (
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
		},
	}

	resources := loreResourceOrder(m)
	if len(resources) != 5 {
		t.Fatalf("loreResourceOrder returned %d resources, want 5", len(resources))
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

	// Role must come before S3 Bucket.
	bucketIdx, hasBucket := byID["fabrica-lore-store-12345"]
	if !hasBucket {
		t.Error("S3 Bucket not found")
	} else if roIdx >= bucketIdx {
		t.Errorf("Role (idx %d) must be deleted before S3 Bucket (idx %d)", roIdx, bucketIdx)
	}

	// S3 Bucket must come before Security Group.
	sgIdx, hasSG := byID["sg-lore123"]
	if !hasSG {
		t.Error("SecurityGroup not found")
	} else if bucketIdx >= sgIdx {
		t.Errorf("S3 Bucket (idx %d) must be deleted before SecurityGroup (idx %d)", bucketIdx, sgIdx)
	}

	// Verify SG is last.
	if sgIdx != 4 {
		t.Errorf("SecurityGroup at index %d, want 4 (last)", sgIdx)
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
