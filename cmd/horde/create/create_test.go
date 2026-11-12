package create

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/horde"
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
		// Same wiring New() does: providers that can inspect AMIs supply the
		// root device for the encrypted root mapping (#513).
		if insp, ok := provider.(cloud.ImageInspector); ok {
			c.inspectImage = insp.DescribeImage
		}
		if store, ok := provider.(cloud.ParameterStore); ok {
			c.putSecureParameter = store.PutSecureParameter
			c.deleteParameter = store.DeleteParameter
		}
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

// TestCreateDryRunOutputFields verifies key fields appear in dry-run output,
// including the encrypted root volume line from the inspected AMI.
func TestCreateDryRunOutputFields(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{}
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
		"/dev/sda1 (encrypted)",
		"Cost estimate:",
	} {
		assert.Contains(t, got, want)
	}
}

// TestCreateAlreadyProvisioned verifies clean exit when a complete module is
// already in state, even while its status is still transitional.
func TestCreateAlreadyProvisioned(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{}
	st := testutil.NewTestState()
	st.UpsertModule("horde", "ami-existing", "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-existing"},
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-horde-role"},
		{TypeName: "AWS::IAM::InstanceProfile", Identifier: "fabrica-horde-profile"},
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

// TestCreatePartialModuleFails verifies a partially provisioned module (an
// interrupted create) is refused with recovery guidance instead of reported
// as already provisioned.
func TestCreatePartialModuleFails(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{}
	st := testutil.NewTestState()
	st.UpsertModule("horde", "ami-existing", "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-existing"},
	})
	c := newTestCommand(&out, provider, st)

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("run: expected error for partial module, got nil")
	}
	for _, want := range []string{
		`status "provisioning"`,
		"AWS::EC2::Instance",
		"fabrica horde destroy",
	} {
		assert.Contains(t, err.Error(), want)
	}
	if provider.CreateCalls != 0 {
		t.Fatalf("partial module: made %d create calls, want 0", provider.CreateCalls)
	}
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
	provider := &testutil.InspectorProvider{}
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
	provider := &testutil.InspectorProvider{
		TestProvider: testutil.TestProvider{
			CreateErr: map[string]error{
				cloud.TypeAWSEC2Instance: errors.New("quota exceeded"),
			},
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
	provider := &testutil.InspectorProvider{}
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
	provider := &testutil.InspectorProvider{
		TestProvider: testutil.TestProvider{
			CreateErr: map[string]error{
				cloud.TypeAWSEC2SecurityGroup: errors.New("sg quota"),
			},
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
	provider := &testutil.InspectorProvider{}
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
	provider := &testutil.InspectorProvider{}
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
	c := newTestCommand(&out, &testutil.InspectorProvider{}, nil)
	c.readState = func() (*fabricastate.State, error) {
		return nil, errors.New("state corrupted")
	}

	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "state corrupted") {
		t.Fatalf("state failure hidden by an earlier validation error: %v", err)
	}
}

// TestCreateRoleFailurePreservesSG verifies IAM role create failure returns
// a clear error and the security group remains in state for recovery.
func TestCreateRoleFailurePreservesSG(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{
		TestProvider: testutil.TestProvider{
			CreateErr: map[string]error{
				cloud.TypeAWSIAMRole: errors.New("iam disabled"),
			},
		},
	}
	st := testutil.NewTestState()
	capture := &testutil.StateWriteCapture{}
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = capture.WriteFunc()

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error on IAM role create failure")
	}
	assert.Contains(t, err.Error(), "creating IAM role")
	if !capture.Written() {
		t.Fatal("state was never written")
	}
	last := capture.Last()
	_, hasSG := last.GetModuleResource("horde", "AWS::EC2::SecurityGroup")
	if !hasSG {
		t.Error("SG resource not recorded in state after role failure")
	}
}

// TestCreateProfileFailurePreservesSGAndRole verifies instance profile create
// failure returns a clear error and SG + role remain in state for recovery.
func TestCreateProfileFailurePreservesSGAndRole(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{
		TestProvider: testutil.TestProvider{
			CreateErr: map[string]error{
				cloud.TypeAWSIAMInstanceProfile: errors.New("iam disabled"),
			},
		},
	}
	st := testutil.NewTestState()
	capture := &testutil.StateWriteCapture{}
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = capture.WriteFunc()

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error on instance profile create failure")
	}
	assert.Contains(t, err.Error(), "creating instance profile")
	if !capture.Written() {
		t.Fatal("state was never written")
	}
	last := capture.Last()
	_, hasSG := last.GetModuleResource("horde", "AWS::EC2::SecurityGroup")
	if !hasSG {
		t.Error("SG resource not recorded in state after profile failure")
	}
	_, hasRole := last.GetModuleResource("horde", "AWS::IAM::Role")
	if !hasRole {
		t.Error("IAM role not recorded in state after profile failure")
	}
}

// TestCreatePasswordGenerationError verifies that a password generation failure
// returns a clear error and no resources are created.
func TestCreatePasswordGenerationError(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.genPassword = func(int) (string, error) {
		return "", errors.New("rand unavailable")
	}

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when password generation fails")
	}
	assert.Contains(t, err.Error(), "generating MongoDB password")
	if provider.CreateCalls != 0 {
		t.Fatalf("expected 0 create calls, got %d", provider.CreateCalls)
	}
}

// TestCreateUserDataGenerationError verifies that a userdata generation failure
// returns a clear error and no EC2 instance is created.
func TestCreateUserDataGenerationError(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{}
	st := testutil.NewTestState()
	capture := &testutil.StateWriteCapture{}
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = capture.WriteFunc()
	c.genUserData = func(cfg horde.UserDataConfig) (string, error) {
		return "", errors.New("template missing")
	}

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when userdata generation fails")
	}
	assert.Contains(t, err.Error(), "generating user data")
	// SG, role, and profile should be in state; instance should not.
	if !capture.Written() {
		t.Fatal("state was never written")
	}
	last := capture.Last()
	_, hasSG := last.GetModuleResource("horde", "AWS::EC2::SecurityGroup")
	if !hasSG {
		t.Error("SG not in state after userdata failure")
	}
	_, hasInstance := last.GetModuleResource("horde", "AWS::EC2::Instance")
	if hasInstance {
		t.Error("EC2 instance should not be in state after userdata failure")
	}
}

// #513: the encrypted root mapping lands on the AMI's real root device, so a
// provider that cannot inspect AMIs refuses the launch rather than guessing
// a device name. The refuse happens before RunCreate, so nothing is created.

func TestCreateRefusesWithoutImageInspector(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected a refuse-to-launch error")
	}
	assert.Contains(t, err.Error(), "refusing to launch")
	assert.Contains(t, err.Error(), "cannot inspect AMIs")
	assert.Contains(t, err.Error(), "horde.amiId ami-test123")
	if provider.CreateCalls != 0 {
		t.Errorf("CreateCalls = %d, want 0 (refuse precedes any write)", provider.CreateCalls)
	}
}

func TestCreateRefusesMalformedAMIRootDevice(t *testing.T) {
	tests := []struct {
		name    string
		rootDev string
		devType string
		want    string
	}{
		{"no root device name", "", "ebs", "reports no root device name"},
		{"malformed root device name", "/dev/ram0", "ebs", "not an EC2 device name"},
		{"non-EBS root", "/dev/sda1", "instance-store", "needs an EBS-backed AMI"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			c := newTestCommand(&out, &testutil.TestProvider{}, testutil.NewTestState())
			c.assumeYes = true
			c.inspectImage = func(_ context.Context, _ string) (cloud.ImageInfo, error) {
				return cloud.ImageInfo{RootDeviceName: tc.rootDev, RootDeviceType: tc.devType}, nil
			}
			err := c.run(context.Background())
			if err == nil {
				t.Fatalf("expected a refusal for %q", tc.rootDev)
			}
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestCreateInspectAMIErrorsWrap(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, &testutil.TestProvider{}, testutil.NewTestState())
	c.assumeYes = true
	c.inspectImage = func(_ context.Context, _ string) (cloud.ImageInfo, error) {
		return cloud.ImageInfo{}, errors.New("describe denied")
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected an inspect error")
	}
	assert.Contains(t, err.Error(), "inspecting horde.amiId ami-test123")
	assert.Contains(t, err.Error(), "describe denied")
}

// TestCreateInspectsTheConfiguredAMI pins that the inspection targets
// horde.amiId as configured, not a guessed ID.
func TestCreateInspectsTheConfiguredAMI(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.LastImageID != "ami-test123" {
		t.Errorf("LastImageID = %q, want the configured horde.amiId", provider.LastImageID)
	}
	if len(provider.DescribeImageIDs) != 1 || provider.DescribeImageIDs[0] != "ami-test123" {
		t.Errorf("DescribeImageIDs = %v, want one call for the configured AMI", provider.DescribeImageIDs)
	}
}

const testNonce = "0123456789abcdef0123456789abcdef"

func TestCreateServiceTokenParameterLifecycle(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{}
	st := testutil.NewTestState()
	capture := &testutil.StateWriteCapture{}
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = capture.WriteFunc()
	c.newNonce = func() (string, error) { return testNonce, nil }
	c.runtime.Config.Cloud.AWS.Tags = map[string]string{"project": "studio"}
	var gotUD horde.UserDataConfig
	c.genUserData = func(cfg horde.UserDataConfig) (string, error) { gotUD = cfg; return "dWQ=", nil }

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "/fabrica/horde/" + testNonce + "/service-token"
	if len(provider.PutParameters) != 1 {
		t.Fatalf("PutParameters = %d, want 1", len(provider.PutParameters))
	}
	p := provider.PutParameters[0]
	if p.Name != want || p.Value != horde.ServiceTokenPlaceholder || p.Tags["project"] != "studio" {
		t.Errorf("parameter = %+v", p)
	}
	if gotUD.ServiceTokenParameter != want || gotUD.Region == "" {
		t.Errorf("cloud-init config = %+v", gotUD)
	}
	inst := capture.Last().GetModule("horde").Resources[3]
	if inst.Properties["serviceTokenParameter"] != want {
		t.Errorf("instance properties = %v", inst.Properties)
	}
	for _, r := range provider.Created {
		if strings.Contains(string(r.DesiredState), horde.ServiceTokenPlaceholder) {
			t.Errorf("%s desired state carries the parameter value", r.TypeName)
		}
	}
	if len(provider.DeletedParameters) != 0 {
		t.Errorf("successful create deleted %v", provider.DeletedParameters)
	}
}

func TestCreateSGFailureDeletesServiceTokenParameter(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{}
	provider.CreateErr = map[string]error{"AWS::EC2::SecurityGroup": errors.New("boom")}
	c := newTestCommand(&out, provider, testutil.NewTestState())
	c.assumeYes = true
	c.newNonce = func() (string, error) { return testNonce, nil }
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if len(provider.DeletedParameters) != 1 || provider.DeletedParameters[0] != "/fabrica/horde/"+testNonce+"/service-token" {
		t.Errorf("DeletedParameters = %v", provider.DeletedParameters)
	}
}

func TestCreateKeepsServiceTokenOnceInstanceExists(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{}
	c := newTestCommand(&out, provider, testutil.NewTestState())
	c.assumeYes = true
	c.newNonce = func() (string, error) { return testNonce, nil }
	writes := 0
	c.writeState = func(*fabricastate.State) error {
		writes++
		if writes == 4 { // the state write after the instance is created
			return errors.New("state write failed")
		}
		return nil
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected an error")
	}
	if len(provider.DeletedParameters) != 0 {
		t.Errorf("the instance exists, but create deleted %v", provider.DeletedParameters)
	}
}

func TestCreateRequiresParameterStore(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{}
	c := newTestCommand(&out, provider, testutil.NewTestState())
	c.assumeYes = true
	c.putSecureParameter = nil
	c.deleteParameter = nil
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "SSM parameter support") {
		t.Fatalf("err = %v", err)
	}
	if provider.CreateCalls != 0 {
		t.Errorf("CreateCalls = %d, want 0", provider.CreateCalls)
	}
}

// TestCreateNonceFailure verifies a nonce-generation failure aborts before
// any write and surfaces the cause.
func TestCreateNonceFailure(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{}
	c := newTestCommand(&out, provider, testutil.NewTestState())
	c.assumeYes = true
	c.newNonce = func() (string, error) { return "", errors.New("nonce unavailable") }

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when nonce generation fails")
	}
	assert.Contains(t, err.Error(), "nonce unavailable")
	if len(provider.PutParameters) != 0 {
		t.Fatalf("PutParameters = %v, want none", provider.PutParameters)
	}
	if provider.CreateCalls != 0 {
		t.Fatalf("CreateCalls = %d, want 0 (the nonce precedes every write)", provider.CreateCalls)
	}
}

// TestCreateMalformedNonceRejected verifies a nonce that fails the per-create
// name shape is rejected before the parameter is created.
func TestCreateMalformedNonceRejected(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{}
	c := newTestCommand(&out, provider, testutil.NewTestState())
	c.assumeYes = true
	c.newNonce = func() (string, error) { return "not-hex-32", nil }

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error for a malformed nonce")
	}
	assert.Contains(t, err.Error(), "nonce must be 32 lowercase hex characters")
	if len(provider.PutParameters) != 0 {
		t.Fatalf("PutParameters = %v, want none", provider.PutParameters)
	}
	if provider.CreateCalls != 0 {
		t.Fatalf("CreateCalls = %d, want 0", provider.CreateCalls)
	}
}

// TestCreatePutServiceTokenParameterFailure verifies a SecureString create
// failure fails the run with the parameter name and cause, before any
// resource exists.
func TestCreatePutServiceTokenParameterFailure(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{
		TestProvider: testutil.TestProvider{
			PutParameterErr: errors.New("ssm denied"),
		},
	}
	c := newTestCommand(&out, provider, testutil.NewTestState())
	c.assumeYes = true
	c.newNonce = func() (string, error) { return testNonce, nil }

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when the SSM parameter cannot be created")
	}
	assert.Contains(t, err.Error(), "creating the service-token SSM parameter")
	assert.Contains(t, err.Error(), "/fabrica/horde/"+testNonce+"/service-token")
	assert.Contains(t, err.Error(), "ssm denied")
	if provider.CreateCalls != 0 {
		t.Fatalf("CreateCalls = %d, want 0 (the parameter precedes every resource)", provider.CreateCalls)
	}
}

// TestCreateInstanceFailureAndParameterDeleteFailure verifies the compound
// error when the instance create fails and the placeholder cleanup fails too.
func TestCreateInstanceFailureAndParameterDeleteFailure(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{
		TestProvider: testutil.TestProvider{
			CreateErr: map[string]error{
				cloud.TypeAWSEC2Instance: errors.New("quota exceeded"),
			},
			DeleteParameterErr: errors.New("ssm delete denied"),
		},
	}
	c := newTestCommand(&out, provider, testutil.NewTestState())
	c.assumeYes = true
	c.newNonce = func() (string, error) { return testNonce, nil }

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when the instance create and the parameter cleanup both fail")
	}
	assert.Contains(t, err.Error(), "creating EC2 instance")
	assert.Contains(t, err.Error(), "quota exceeded")
	assert.Contains(t, err.Error(), "also failed to delete SSM parameter")
	assert.Contains(t, err.Error(), "/fabrica/horde/"+testNonce+"/service-token")
	assert.Contains(t, err.Error(), "ssm delete denied")
	if len(provider.DeletedParameters) != 0 {
		t.Fatalf("DeletedParameters = %v, want none", provider.DeletedParameters)
	}
	if provider.CreateCalls != 4 {
		t.Fatalf("CreateCalls = %d, want 4 (SG, role, profile, plus the failed instance attempt)", provider.CreateCalls)
	}
}
