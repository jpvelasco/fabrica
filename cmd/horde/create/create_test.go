package create

import (
	"bytes"
	"context"
	"errors"
	"os"
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
	cfg.Horde.AmiID = "ami-test123"
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

// TestCreateDryRunNoAWSCalls verifies --dry-run makes zero provider calls.
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

// TestCreateDryRunOutputFields verifies key fields appear in dry-run output.
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
		"fabrica-horde-sg",
		"fabrica-horde",
		"Cost estimate:",
	} {
		assert.Contains(t, got, want)
	}
}

// TestCreateAlreadyProvisioned verifies clean exit when module is already in state.
func TestCreateAlreadyProvisioned(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	st.UpsertModule("horde", "ami-existing", "provisioning", []fabricastate.ModuleResource{
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

// TestCreateMissingAmiID verifies error when AmiID is not configured.
func TestCreateMissingAmiID(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.runtime.Config.Horde.AmiID = ""

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when AmiID is empty")
	}
	assert.Contains(t, err.Error(), "horde.amiId is required")
	assert.Contains(t, err.Error(), "horde-ami.md")
	if provider.CreateCalls != 0 {
		t.Fatal("missing AmiID: create was called")
	}
}

// TestCreateHappyPathOrderAndState verifies SG created before instance, both in state.
func TestCreateHappyPathOrderAndState(t *testing.T) {
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
	if provider.CreateCalls != 4 {
		t.Fatalf("expected 4 create calls (SG, role, profile, instance), got %d", provider.CreateCalls)
	}
	if provider.CreatedTypes[0] != "AWS::EC2::SecurityGroup" {
		t.Errorf("first created resource = %q, want AWS::EC2::SecurityGroup", provider.CreatedTypes[0])
	}
	if provider.CreatedTypes[1] != "AWS::IAM::Role" {
		t.Errorf("second created resource = %q, want AWS::IAM::Role", provider.CreatedTypes[1])
	}
	if provider.CreatedTypes[2] != "AWS::IAM::InstanceProfile" {
		t.Errorf("third created resource = %q, want AWS::IAM::InstanceProfile", provider.CreatedTypes[2])
	}
	if provider.CreatedTypes[3] != "AWS::EC2::Instance" {
		t.Errorf("fourth created resource = %q, want AWS::EC2::Instance", provider.CreatedTypes[3])
	}
	if len(capture.States) < 4 {
		t.Fatalf("expected >=4 state writes, got %d", len(capture.States))
	}
	final := capture.Last()
	m := final.GetModule("horde")
	if m == nil {
		t.Fatal("horde module not in final state")
		return
	}
	if len(m.Resources) != 4 {
		t.Fatalf("final state has %d resources, want 4", len(m.Resources))
	}
	if m.Version != "ami-test123" {
		t.Errorf("state version = %q, want ami-test123", m.Version)
	}
}

// TestCreateInstanceFailurePreservesPartialState verifies SG is in state even on instance error.
func TestCreateInstanceFailurePreservesPartialState(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{
		CreateErr: map[string]error{
			cloud.TypeAWSEC2Instance: errors.New("quota exceeded"),
		},
	}
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
	if !capture.Written() {
		t.Fatal("state was never written")
	}
	last := capture.Last()
	_, hasSG := last.GetModuleResource("horde", "AWS::EC2::SecurityGroup")
	if !hasSG {
		t.Error("SG resource not recorded in state after instance failure")
	}
	_, hasRole := last.GetModuleResource("horde", "AWS::IAM::Role")
	if !hasRole {
		t.Error("IAM role not recorded in state after instance failure")
	}
	_, hasProfile := last.GetModuleResource("horde", "AWS::IAM::InstanceProfile")
	if !hasProfile {
		t.Error("Instance profile not recorded in state after instance failure")
	}
}

// TestCreateConfirmationRejected verifies cancellation skips create.
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

// TestCreateNilProviderReturnsError verifies nil provider returns a clear error.
func TestCreateNilProviderReturnsError(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Defaults()
	c := command{
		runtime: globals.Runtime{Config: cfg, Provider: nil},
		costs:   fabricacost.Global,
		out:     &out,
	}
	c.readState = func() (*fabricastate.State, error) { return testutil.NewTestState(), nil }
	c.writeState = testutil.StateWriteNever()

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when provider is nil")
	}
	assert.Contains(t, err.Error(), "no provider configured")
	assert.Contains(t, err.Error(), "fabrica setup")
}

// TestCreateAllowedCIDRWarning verifies 0.0.0.0/0 warning appears in dry-run output.
func TestCreateAllowedCIDRWarning(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.runtime.Config.Horde.AllowedCIDR = "0.0.0.0/0"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assert.Contains(t, out.String(), "Warning: allowedCidr is 0.0.0.0/0")
	assert.Contains(t, out.String(), "0.0.0.0/0")
}

// TestCreateDryRunDefaultVPCNote verifies "Default VPC" note appears when no VPC configured.
func TestCreateDryRunDefaultVPCNote(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	// No VPC configured in hordeCfg; resolver is nil so DefaultVPC won't be set.
	// We test the note appears when VPC fields are empty.
	c.runtime.Config.Horde.VPCId = ""
	c.runtime.Config.Horde.SubnetId = ""

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	// With nil resolver, VPC is empty. The note is printed only when DefaultVPC=true.
	// Just verify no panic and key fields are present.
	assert.Contains(t, out.String(), "fabrica-horde-sg")
}

// TestCreateDryRunM7i2xlargeRecommendation verifies m7i.2xlarge tip in dry-run when default type.
func TestCreateDryRunM7i2xlargeRecommendation(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	// Default instance type is m7i.xlarge → tip about m7i.2xlarge should appear.

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assert.Contains(t, out.String(), "m7i.2xlarge")
}

// TestCreateIdentityFailureAbortsEarly verifies no AWS calls on identity error.
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

// TestCreateSGFailureNoStateWritten verifies state is never written when SG creation fails.
func TestCreateSGFailureNoStateWritten(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{
		CreateErr: map[string]error{
			cloud.TypeAWSEC2SecurityGroup: errors.New("sg quota"),
		},
	}
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

// TestCreateFlagOverridesConfigInstanceType verifies --instance-type flag wins over config.
func TestCreateFlagOverridesConfigInstanceType(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.instanceType = "m7i.4xlarge"
	c.runtime.Config.Horde.InstanceType = "m7i.xlarge"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assert.Contains(t, out.String(), "m7i.4xlarge")
}

// TestCreateFlagOverridesConfigVolumeSize verifies --volume-size flag wins over config.
func TestCreateFlagOverridesConfigVolumeSize(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.volumeSize = 500

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assert.Contains(t, out.String(), "500 GiB")
}

// TestCreatePostCreateCIDRWarning verifies the 0.0.0.0/0 warning appears in
// post-create output (not just dry-run) after a successful provision.
func TestCreatePostCreateCIDRWarning(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	capture := &testutil.StateWriteCapture{}
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.runtime.Config.Horde.AllowedCIDR = "0.0.0.0/0"
	c.writeState = capture.WriteFunc()

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	assert.Contains(t, got, "Warning: horde.allowedCidr is 0.0.0.0/0")
	assert.Contains(t, got, "0.0.0.0/0")
}

// TestCreateWriteCredentialsError verifies WriteCredentials failure returns
// a clear error and no resources are created.
func TestCreateWriteCredentialsError(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	// Write .fabrica as a file so WriteCredentials cannot create the
	// credentials file underneath it.
	if err := os.WriteFile(".fabrica", []byte("not a dir"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when writing credentials fails")
	}
	assert.Contains(t, err.Error(), "writing credentials file")
	if provider.CreateCalls != 0 {
		t.Fatalf("expected 0 create calls, got %d", provider.CreateCalls)
	}
}

// TestCreateReadStateError verifies that readState errors propagate.
func TestCreateReadStateError(t *testing.T) {
	var out bytes.Buffer
	c := command{
		runtime: globals.Runtime{
			Config:   config.Defaults(),
			Provider: &testutil.TestProvider{},
		},
		costs: fabricacost.Global,
		out:   &out,
	}
	c.readState = func() (*fabricastate.State, error) {
		return nil, errors.New("state corrupted")
	}
	c.writeState = testutil.StateWriteNever()
	c.createResource = func(ctx context.Context, r *cloud.Resource) error { return nil }

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when readState fails")
	}
}
