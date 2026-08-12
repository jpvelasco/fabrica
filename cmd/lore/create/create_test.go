package create

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func newTestCommand(out *bytes.Buffer, provider cloud.Provider, st *fabricastate.State) command {
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.State.Table = "fabrica-locks-test"
	cfg.Lore.AmiID = "ami-test123"
	c := command{
		runtime: globals.Runtime{
			Config:   cfg,
			Provider: provider,
		},
		costs:   fabricacost.Global,
		out:     out,
		confirm: func(_, _ string) bool { return true },
	}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	c.writeState = testutil.StateWriteNever()
	if provider != nil {
		c.createResource = provider.Resources().Create
	}
	return c
}

func TestCreateDryRunNoAWSCalls(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.CreateCalls != 0 {
		t.Fatalf("dry-run made %d create calls, want 0", provider.CreateCalls)
	}
}

func TestCreateDryRunOutputFields(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"123456789012",
		"us-east-1",
		"fabrica-lore-sg",
		"fabrica-lore",
		"41337",
		"41339",
		"Cost estimate:",
	} {
		assert.Contains(t, got, want)
	}
}

func TestCreateAlreadyProvisioned(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	st.UpsertModule("lore", "ami-existing", "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-existing"},
		{TypeName: "AWS::EC2::Instance", Identifier: "i-existing"},
	})
	c := newTestCommand(&out, provider, st)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.CreateCalls != 0 {
		t.Fatalf("already-exists: made %d create calls, want 0", provider.CreateCalls)
	}
	assert.Contains(t, out.String(), "already provisioned")
}

func TestCreateMissingAmiID(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.runtime.Config.Lore.AmiID = ""

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when AmiID is empty")
	}
	assert.Contains(t, err.Error(), "lore.amiId is required")
	assert.Contains(t, err.Error(), "lore-ami.md")
	if provider.CreateCalls != 0 {
		t.Fatal("missing AmiID: create was called")
	}
}

func TestCreateHappyPathOrderAndState(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	capture := &testutil.StateWriteCapture{}
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = capture.WriteFunc()

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.CreateCalls != 2 {
		t.Fatalf("expected 2 create calls, got %d", provider.CreateCalls)
	}
	if provider.CreatedTypes[0] != "AWS::EC2::SecurityGroup" {
		t.Errorf("first created = %q", provider.CreatedTypes[0])
	}
	if provider.CreatedTypes[1] != "AWS::EC2::Instance" {
		t.Errorf("second created = %q", provider.CreatedTypes[1])
	}
	if len(capture.States) < 2 {
		t.Fatalf("expected >=2 state writes, got %d", len(capture.States))
	}
	final := capture.Last()
	m := final.GetModule("lore")
	if m == nil {
		t.Fatal("lore module not in final state")
		return
	}
	if len(m.Resources) != 2 {
		t.Fatalf("final state has %d resources, want 2", len(m.Resources))
	}
	if m.Version != "ami-test123" {
		t.Errorf("state version = %q, want ami-test123", m.Version)
	}
	assert.Contains(t, out.String(), "Lore server provisioned")
}

func TestCreateInstanceFailurePreservesPartialState(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	provider := &testutil.TestProvider{CreateErr: map[string]error{cloud.TypeAWSEC2Instance: errors.New("quota exceeded")}}
	st := testutil.NewTestState()
	capture := &testutil.StateWriteCapture{}
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = capture.WriteFunc()

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error on instance create failure")
	}
	assert.Contains(t, err.Error(), "creating EC2 instance")
	if capture.Last() == nil {
		t.Fatal("state was never written")
	}
	_, hasSG := capture.Last().GetModuleResource("lore", "AWS::EC2::SecurityGroup")
	if !hasSG {
		t.Error("SG resource not recorded in state after instance failure")
	}
}

func TestCreateConfirmationRejected(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.confirm = func(_, _ string) bool { return false }

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.CreateCalls != 0 {
		t.Fatalf("cancelled: made %d create calls, want 0", provider.CreateCalls)
	}
	assert.Contains(t, out.String(), "Cancelled")
}

func TestCreateNilProviderReturnsError(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Defaults()
	c := command{
		runtime: globals.Runtime{Config: cfg, Provider: nil},
		costs:   fabricacost.Global,
		out:     &out,
	}
	c.readState = func() (*fabricastate.State, error) { return testutil.NewTestStateWith("", ""), nil }
	c.writeState = testutil.StateWriteNever()

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when provider is nil")
	}
	assert.Contains(t, err.Error(), "no provider configured")
}

func TestCreateAllowedCIDRWarning(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.runtime.Config.Lore.AllowedCIDR = "0.0.0.0/0"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assert.Contains(t, out.String(), "Warning: allowedCidr is 0.0.0.0/0")
	assert.Contains(t, out.String(), "0.0.0.0/0")
}

func TestCreateIdentityFailureAbortsEarly(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{IdentityErr: errors.New("credentials unavailable")}
	st := testutil.NewTestStateWith("", "")
	c := newTestCommand(&out, provider, st)

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when identity fails")
	}
	if provider.CreateCalls != 0 {
		t.Fatal("identity failure: create was called")
	}
	assert.Contains(t, err.Error(), "could not resolve AWS identity")
}

func TestCreateSGFailureNoStateWritten(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	provider := &testutil.TestProvider{CreateErr: map[string]error{cloud.TypeAWSEC2SecurityGroup: errors.New("sg quota")}}
	st := testutil.NewTestState()
	stateWritten := false
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = func(_ *fabricastate.State) error {
		stateWritten = true
		return nil
	}

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error on SG create failure")
	}
	assert.Contains(t, err.Error(), "creating security group")
	if stateWritten {
		t.Error("state must not be written when SG creation fails")
	}
}

func TestCreateFlagOverridesConfig(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.instanceType = "m5.2xlarge"
	c.volumeSize = 1000

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assert.Contains(t, out.String(), "m5.2xlarge")
	assert.Contains(t, out.String(), "1000 GiB")
}

func TestCreateS3StoreDryRun(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.runtime.Config.Lore.StoreBackend = "s3"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	assert.Contains(t, got, "Store backend:    s3")
	assert.Contains(t, got, "S3 Bucket")
	assert.Contains(t, got, "IAM Role")
	assert.Contains(t, got, "Instance Profile")
	assert.Contains(t, got, "fabrica-lore-store")
}

func TestCreateS3StoreHappyPath(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	capture := &testutil.StateWriteCapture{}
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = capture.WriteFunc()
	c.runtime.Config.Lore.StoreBackend = "s3"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	// SG + S3 bucket + IAM role + instance profile + instance = 5 creates
	if provider.CreateCalls != 5 {
		t.Fatalf("expected 5 create calls for S3 store, got %d", provider.CreateCalls)
	}
	// Verify creation order: SG -> S3 bucket -> IAM role -> instance profile -> instance
	if provider.CreatedTypes[0] != "AWS::EC2::SecurityGroup" {
		t.Errorf("first created = %q, want SG", provider.CreatedTypes[0])
	}
	if provider.CreatedTypes[1] != "AWS::S3::Bucket" {
		t.Errorf("second created = %q, want S3 bucket", provider.CreatedTypes[1])
	}
	if provider.CreatedTypes[2] != "AWS::IAM::Role" {
		t.Errorf("third created = %q, want IAM role", provider.CreatedTypes[2])
	}
	if provider.CreatedTypes[3] != "AWS::IAM::InstanceProfile" {
		t.Errorf("fourth created = %q, want instance profile", provider.CreatedTypes[3])
	}
	if provider.CreatedTypes[4] != "AWS::EC2::Instance" {
		t.Errorf("fifth created = %q, want instance", provider.CreatedTypes[4])
	}
	final := capture.Last()
	m := final.GetModule("lore")
	if m == nil {
		t.Fatal("lore module not in final state")
		return
	}
	if len(m.Resources) != 5 {
		t.Fatalf("final state has %d resources, want 5", len(m.Resources))
	}
}

func TestCreateS3StoreBucketFailurePreservesPartialState(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	provider := &testutil.TestProvider{CreateErr: map[string]error{cloud.TypeAWSS3Bucket: errors.New("bucket name taken")}}
	st := testutil.NewTestState()
	capture := &testutil.StateWriteCapture{}
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = capture.WriteFunc()
	c.runtime.Config.Lore.StoreBackend = "s3"

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error on S3 bucket create failure")
	}
	assert.Contains(t, err.Error(), "creating S3 store bucket")
	// SG should be in state; bucket should not.
	if capture.Last() == nil {
		t.Fatal("state was never written")
	}
	_, hasSG := capture.Last().GetModuleResource("lore", "AWS::EC2::SecurityGroup")
	if !hasSG {
		t.Error("SG resource not recorded in state after bucket failure")
	}
}
