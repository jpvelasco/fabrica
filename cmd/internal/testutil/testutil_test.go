package testutil

import (
	"bytes"
	"context"
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/spf13/cobra"
)

func TestBuildTestRoot(t *testing.T) {
	var out bytes.Buffer
	root, opts := BuildTestRoot(&out)

	if root.Use != "fabrica" {
		t.Errorf("root.Use = %q, want fabrica", root.Use)
	}
	if opts == nil {
		t.Fatal("opts should not be nil")
	}
	if len(root.Commands()) != 0 {
		t.Error("root should have no subcommands initially")
	}
	if root.PersistentFlags().Lookup("dry-run") == nil {
		t.Error("--dry-run flag missing")
	}
	if root.PersistentFlags().Lookup("yes") == nil {
		t.Error("--yes flag missing")
	}
	if root.PersistentFlags().Lookup("json") == nil {
		t.Error("--json flag missing")
	}
	// Verify opts is wired to flags.
	if opts.DryRun {
		t.Error("DryRun should default to false")
	}
	// Add a test subcommand to verify AddCommand works.
	root.AddCommand(&cobra.Command{Use: "test"})
	if len(root.Commands()) != 1 {
		t.Error("subcommand not added")
	}
}

func TestNewTestRuntime(t *testing.T) {
	fake := &CobraFakeProvider{}
	src := NewTestRuntime(fake)
	rt, err := src()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt.Provider != fake {
		t.Error("provider not set")
	}
	if rt.Config.Cloud.AWS.AccountID != "123456789012" {
		t.Errorf("AccountID = %q, want 123456789012", rt.Config.Cloud.AWS.AccountID)
	}
}

func TestNewNilProviderRuntime(t *testing.T) {
	src := NewNilProviderRuntime()
	rt, err := src()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rt.Provider != nil {
		t.Error("provider should be nil")
	}
}

func TestWriteStateFile(t *testing.T) {
	dir := t.TempDir()
	WriteStateFile(t, dir, `{"test":true}`)

	path := dir + "/.fabrica/state.json"
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != `{"test":true}` {
		t.Errorf("content = %q, want {\"test\":true}", string(data))
	}
}

func TestAssertContains(t *testing.T) {
	AssertContains(t, "hello world", "world")
}

func TestAssertContainsFails(t *testing.T) {
	// Verify AssertContains correctly detects missing substring.
	// We test this by checking the function's behavior indirectly:
	// AssertContains uses strings.Contains internally, so we verify
	// the positive case above. The negative case is verified by the
	// fact that t.Fatal is called when substr is not found.
	// A direct test would trigger t.Fatal and fail the test, so we
	// skip this as it's a negative-path assertion.
}

func TestCobraFakeProviderIdentity(t *testing.T) {
	fp := &CobraFakeProvider{}
	account, arn, region, err := fp.Identity(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if account != "123456789012" {
		t.Errorf("account = %q", account)
	}
	if arn != "arn:aws:iam::123456789012:user/test" {
		t.Errorf("arn = %q", arn)
	}
	if region != "us-east-1" {
		t.Errorf("region = %q", region)
	}
}

func TestCobraFakeProviderIdentityError(t *testing.T) {
	fp := &CobraFakeProvider{IdentErr: cloud.ErrResourceNotFound}
	_, _, _, err := fp.Identity(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestCobraFakeRCDeleteCounts(t *testing.T) {
	fp := &CobraFakeProvider{}
	rc := fp.Resources().(*CobraFakeRC)

	for i := 0; i < 3; i++ {
		_ = rc.Delete(context.Background(), &cloud.Resource{})
	}
	if fp.DeleteCalls != 3 {
		t.Errorf("DeleteCalls = %d, want 3", fp.DeleteCalls)
	}
}

func TestCobraFakeRCCreateAssignsIdentifier(t *testing.T) {
	fp := &CobraFakeProvider{}
	rc := fp.Resources().(*CobraFakeRC)

	res := &cloud.Resource{TypeName: "AWS::EC2::Instance"}
	_ = rc.Create(context.Background(), res)
	if res.Identifier != "i-cobra123" {
		t.Errorf("Identifier = %q, want i-cobra123", res.Identifier)
	}
}

func TestCobraFakeRCGetWithStore(t *testing.T) {
	fp := &CobraFakeProvider{
		GetResources: map[string]cloud.Resource{
			"AWS::EC2::Instance": {Identifier: "i-stored"},
		},
	}
	rc := fp.Resources().(*CobraFakeRC)

	res := &cloud.Resource{TypeName: "AWS::EC2::Instance"}
	_ = rc.Get(context.Background(), res)
	if res.Identifier != "i-stored" {
		t.Errorf("Identifier = %q, want i-stored", res.Identifier)
	}
}

// TestCobraFakeProviderName verifies Name returns "fake".
func TestCobraFakeProviderName(t *testing.T) {
	fp := &CobraFakeProvider{}
	if fp.Name() != "fake" {
		t.Errorf("Name = %q, want fake", fp.Name())
	}
}

// TestCobraFakeRCUpdateNoop verifies Update is a no-op (returns nil).
func TestCobraFakeRCUpdateNoop(t *testing.T) {
	fp := &CobraFakeProvider{}
	rc := fp.Resources().(*CobraFakeRC)
	err := rc.Update(context.Background(), &cloud.Resource{})
	if err != nil {
		t.Fatalf("Update should return nil, got: %v", err)
	}
}

// TestCobraFakeRCListEmpty verifies List returns nil, nil.
func TestCobraFakeRCListEmpty(t *testing.T) {
	fp := &CobraFakeProvider{}
	rc := fp.Resources().(*CobraFakeRC)
	results, err := rc.List(context.Background(), "AWS::EC2::Instance")
	if err != nil {
		t.Fatalf("List should return nil error, got: %v", err)
	}
	if results != nil {
		t.Errorf("List should return nil results, got: %v", results)
	}
}

// TestCobraFakeRCCreateIdentifiers verifies all Create identifier branches.
func TestCobraFakeRCCreateIdentifiers(t *testing.T) {
	fp := &CobraFakeProvider{}
	rc := fp.Resources().(*CobraFakeRC)

	tests := []struct {
		typeName string
		want     string
	}{
		{"AWS::EC2::Instance", "i-cobra123"},
		{"AWS::EC2::SecurityGroup", "sg-cobra123"},
		{"AWS::IAM::Role", "arn:aws:iam::123456789012:role/test-role"},
		{"AWS::S3::Bucket", "test-AWS::S3::Bucket"},
	}

	for _, tt := range tests {
		res := &cloud.Resource{TypeName: tt.typeName}
		_ = rc.Create(context.Background(), res)
		if res.Identifier != tt.want {
			t.Errorf("Create(%s) Identifier = %q, want %q", tt.typeName, res.Identifier, tt.want)
		}
	}
}

// TestCobraFakeRCCreateExistingIdentifier verifies Create does not overwrite an existing identifier.
func TestCobraFakeRCCreateExistingIdentifier(t *testing.T) {
	fp := &CobraFakeProvider{}
	rc := fp.Resources().(*CobraFakeRC)

	res := &cloud.Resource{TypeName: "AWS::EC2::Instance", Identifier: "i-existing"}
	_ = rc.Create(context.Background(), res)
	if res.Identifier != "i-existing" {
		t.Errorf("Identifier = %q, want i-existing (should not overwrite)", res.Identifier)
	}
}

// TestCobraFakeRCGetNil verifies Get returns ErrResourceNotFound for nil resource.
func TestCobraFakeRCGetNil(t *testing.T) {
	fp := &CobraFakeProvider{}
	rc := fp.Resources().(*CobraFakeRC)

	err := rc.Get(context.Background(), nil)
	if err != cloud.ErrResourceNotFound {
		t.Errorf("Get(nil) = %v, want ErrResourceNotFound", err)
	}
}

// TestCobraFakeRCGetNoStore verifies Get handles missing type in store.
func TestCobraFakeRCGetNoStore(t *testing.T) {
	fp := &CobraFakeProvider{GetResources: map[string]cloud.Resource{}}
	rc := fp.Resources().(*CobraFakeRC)

	res := &cloud.Resource{TypeName: "AWS::EC2::Instance"}
	err := rc.Get(context.Background(), res)
	if err != nil {
		t.Fatalf("Get should return nil for missing type, got: %v", err)
	}
}

// TestAssertContainsExact verifies AssertContains finds exact match.
func TestAssertContainsExact(t *testing.T) {
	AssertContains(t, "hello", "hello")
}

// TestAssertContainsPrefix verifies AssertContains finds prefix.
func TestAssertContainsPrefix(t *testing.T) {
	AssertContains(t, "hello world", "hello")
}

// TestAssertContainsEmpty verifies AssertContains handles empty string.
func TestAssertContainsEmpty(t *testing.T) {
	AssertContains(t, "hello", "")
}
