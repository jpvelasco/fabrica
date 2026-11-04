package create

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/perforce"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// newTestCommand builds a command with fake seams for white-box testing.
func newTestCommand(out *bytes.Buffer, provider cloud.Provider, st *fabricastate.State) command {
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.State.Table = "fabrica-locks-test"
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
	c.resolveAMI = func(_ context.Context, _ string) (string, error) { return "ami-fake-ubuntu", nil }
	if provider != nil {
		c.createResource = provider.Resources().Create
		// Same wiring New() does: providers that can inspect AMIs supply the
		// root device for the encrypted root mapping (#513).
		if insp, ok := provider.(cloud.ImageInspector); ok {
			c.inspectImage = insp.DescribeImage
		}
	}
	return c
}

func TestCreateDryRunDefaultVPCNote(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, &testutil.TestProvider{}, testutil.NewTestState())
	c.printDryRun(&perforce.CreatePlan{
		Account: "1", Region: "us-east-1", InstanceType: "m5.xlarge",
		HelixVersion: "2024.2", VolumeSize: 500, DefaultVPC: true, VPCID: "vpc-def",
		SGName: "sg", RoleName: "role", InstanceProfileName: "prof", InstanceName: "inst",
	})
	assert.Contains(t, out.String(), "default")
	assert.Contains(t, out.String(), "vpc-def")
	assert.Contains(t, out.String(), "Default VPC used")
}

func TestCreateDryRunExplicitVPCNoDefault(t *testing.T) {
	var out bytes.Buffer
	c := newTestCommand(&out, &testutil.TestProvider{}, testutil.NewTestState())
	c.printDryRun(&perforce.CreatePlan{
		Account: "1", Region: "us-east-1", InstanceType: "m5.xlarge",
		HelixVersion: "2024.2", VolumeSize: 500, DefaultVPC: false, VPCID: "vpc-explicit",
		SGName: "sg", RoleName: "role", InstanceProfileName: "prof", InstanceName: "inst",
	})
	assert.Contains(t, out.String(), "vpc-explicit")
	if strings.Contains(out.String(), "Default VPC used") {
		t.Fatal("should not print default VPC note")
	}
}

func TestCreatePasswordGenError(t *testing.T) {
	c := newTestCommand(&bytes.Buffer{}, &testutil.TestProvider{}, testutil.NewTestState())
	c.assumeYes = true
	c.genPassword = func(int) (string, error) { return "", errors.New("entropy fail") }
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "generating admin password") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateWriteCredentialsError(t *testing.T) {
	c := newTestCommand(&bytes.Buffer{}, &testutil.TestProvider{}, testutil.NewTestState())
	c.assumeYes = true
	c.writeCreds = func(string, string) error { return errors.New("perm denied") }
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "writing credentials") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateProceedingWithoutConfirmMessage(t *testing.T) {
	var out bytes.Buffer
	// already provisioned exits early after assumeYes path isn't hit;
	// exercise assumeYes message via create on empty state
	c := newTestCommand(&out, &testutil.InspectorProvider{}, testutil.NewTestState())
	c.assumeYes = true
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	assert.Contains(t, out.String(), "Proceeding without interactive confirmation")
}

func TestCreateDryRunLatestVersionLabel(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.version = "latest"
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "latest") {
		t.Fatalf("out: %s", got)
	}
	// "latest" should not get " (pinned)" suffix
	if strings.Contains(got, "latest (pinned)") {
		t.Fatalf("latest should not be labeled pinned: %s", got)
	}
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
		"fabrica-perforce-sg",
		"fabrica-perforce",
		"Cost estimate:",
	} {
		assert.Contains(t, got, want)
	}
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

	// Create order: SG → IAM role → instance profile → instance
	wantTypes := []string{
		"AWS::EC2::SecurityGroup",
		"AWS::IAM::Role",
		"AWS::IAM::InstanceProfile",
		"AWS::EC2::Instance",
	}
	for i, want := range wantTypes {
		if provider.CreatedTypes[i] != want {
			t.Errorf("CreatedTypes[%d] = %q, want %q", i, provider.CreatedTypes[i], want)
		}
	}

	// State must have been written after each resource
	if len(capture.States) < 4 {
		t.Fatalf("expected >=4 state writes, got %d", len(capture.States))
	}

	// Final state must have all resources
	final := capture.Last()
	m := final.GetModule("perforce")
	if m == nil {
		t.Fatal("perforce module not in final state")
		return
	}
	if len(m.Resources) != 4 {
		t.Fatalf("final state has %d resources, want 4", len(m.Resources))
	}
	// The instance record carries cost-relevant Properties so cost report can
	// read the deployed shape from state, not just config. Also verify the
	// resolved AMI ID is recorded for drift/export fidelity.
	inst, ok := final.GetModuleResource("perforce", "AWS::EC2::Instance")
	if !ok {
		t.Fatal("instance resource missing from final state")
	}
	if inst.Properties["instanceType"] == "" || inst.Properties["volumeSize"] == "" {
		t.Errorf("instance Properties missing cost metadata: %+v", inst.Properties)
	}
	if !strings.HasPrefix(inst.Properties["imageId"], "ami-") {
		t.Errorf("instance Properties.imageId = %q, want ami-*", inst.Properties["imageId"])
	}
}

func TestCreateWriteStateFailsAfterRole(t *testing.T) {
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&bytes.Buffer{}, provider, st)
	c.assumeYes = true
	c.writeState = testutil.StateWriteError(2)
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "writing state after IAM role") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateWriteStateFailsAfterInstance(t *testing.T) {
	provider := &testutil.InspectorProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&bytes.Buffer{}, provider, st)
	c.assumeYes = true
	c.writeState = testutil.StateWriteError(4)
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "writing state after Instance") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateWriteStateFailsAfterProfile(t *testing.T) {
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&bytes.Buffer{}, provider, st)
	c.assumeYes = true
	c.writeState = testutil.StateWriteError(3)
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "writing state after Instance profile") {
		t.Fatalf("err = %v", err)
	}
}

func TestCreateRoleFailureAfterSG(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{CreateErr: map[string]error{cloud.TypeAWSIAMRole: errors.New("iam denied")}}
	st := testutil.NewTestState()
	capture := &testutil.StateWriteCapture{}
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = capture.WriteFunc()
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "IAM role") {
		t.Fatalf("err = %v", err)
	}
	if capture.Last() == nil {
		t.Fatal("expected state write after SG")
	}
	if _, ok := capture.Last().GetModuleResource("perforce", "AWS::EC2::SecurityGroup"); !ok {
		t.Fatal("SG should be in state")
	}
}

func TestCreateProfileFailureAfterRole(t *testing.T) {
	provider := &testutil.TestProvider{CreateErr: map[string]error{cloud.TypeAWSIAMInstanceProfile: errors.New("profile denied")}}
	st := testutil.NewTestState()
	c := newTestCommand(&bytes.Buffer{}, provider, st)
	c.assumeYes = true
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "instance profile") {
		t.Fatalf("err = %v", err)
	}
}

// TestCreateInstanceFailurePreservesPartialState verifies SG is in state even on instance error.
func TestCreateInstanceFailurePreservesPartialState(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{TestProvider: testutil.TestProvider{CreateErr: map[string]error{cloud.TypeAWSEC2Instance: errors.New("quota exceeded")}}}
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

	// SG identifier must be recorded even though instance failed
	if capture.Last() == nil {
		t.Fatal("state was never written")
	}
	_, hasSG := capture.Last().GetModuleResource("perforce", "AWS::EC2::SecurityGroup")
	if !hasSG {
		t.Error("SG resource not recorded in state after instance failure")
	}
}

// TestCreateConfirmationRejectedNoAWSCalls verifies cancellation skips create.
func TestCreateConfirmationRejectedNoAWSCalls(t *testing.T) {
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
	c.readState = func() (*fabricastate.State, error) { return testutil.NewTestStateWith("", ""), nil }
	c.writeState = testutil.StateWriteNever()

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when provider is nil")
	}
	assert.Contains(t, err.Error(), "no provider configured")
	assert.Contains(t, err.Error(), "fabrica setup")
}

// TestCreateVersionFlagInvalidAbortsBeforeAWS verifies bad version errors early.
func TestCreateVersionFlagInvalidAbortsBeforeAWS(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.version = "bad-version"

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid version")
	}
	if provider.CreateCalls != 0 {
		t.Fatal("invalid version: create was called")
	}
}

func TestCreateLockHeldAborts(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.LockingProvider{TestProvider: &testutil.TestProvider{}, Held: true}
	c := newTestCommand(&out, provider, testutil.NewTestState())
	c.assumeYes = true
	c.readState = func() (*fabricastate.State, error) {
		t.Fatal("should not read state when lock is held")
		return nil, nil
	}

	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "another fabrica run holds the state lock") {
		t.Fatalf("err = %v, want held-lock abort", err)
	}
	if provider.CreateCalls != 0 {
		t.Fatal("held lock: create was called")
	}
}

// TestCreateReadStateError verifies error is surfaced before any AWS call.
func TestCreateReadStateError(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	c := newTestCommand(&out, provider, nil)
	c.readState = func() (*fabricastate.State, error) {
		return nil, errors.New("disk read failure")
	}

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when readState fails")
	}
	if provider.CreateCalls != 0 {
		t.Fatal("readState failure: create was called")
	}
	assert.Contains(t, err.Error(), "reading state")
}

// TestCreateSGFailureNoStateWritten verifies state is never written when SG creation fails.
func TestCreateSGFailureNoStateWritten(t *testing.T) {
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

// TestCreateDryRunVersionPinned verifies "(pinned)" label appears for non-latest version.
func TestCreateDryRunVersionPinned(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.version = "2024.2"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assert.Contains(t, out.String(), "(pinned)")
}

// TestCreateDryRunVersionLatestNotPinned verifies "latest" does not get "(pinned)" label.
func TestCreateDryRunVersionLatestNotPinned(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.version = "latest"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	assert.Contains(t, got, "latest")
	if strings.Contains(got, "(pinned)") {
		t.Error("'latest' should not show '(pinned)' label")
	}
}

// TestCreateFlagOverridesConfigInstanceType verifies --instance-type flag wins over config.
func TestCreateFlagOverridesConfigInstanceType(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.instanceType = "c5.2xlarge"
	c.runtime.Config.Perforce.InstanceType = "m5.xlarge"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assert.Contains(t, out.String(), "c5.2xlarge")
}

// TestCreateFlagOverridesConfigVolumeSize verifies --volume-size flag wins over config.
func TestCreateFlagOverridesConfigVolumeSize(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.volumeSize = 1000
	c.runtime.Config.Perforce.VolumeSize = 500

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assert.Contains(t, out.String(), "1000 GiB")
}

// TestCreateVersionFromConfig verifies version is read from config when flag is empty.
func TestCreateVersionFromConfig(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.version = ""
	c.runtime.Config.Perforce.Version = "2025.1"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assert.Contains(t, out.String(), "2025.1")
}

// TestCreateAlreadyProvisioned verifies clean exit when a complete module is
// already in state, even while its status is still transitional.
func TestCreateAlreadyProvisioned(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	st.UpsertModule("perforce", "2025.2", "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-existing"},
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-perforce-role"},
		{TypeName: "AWS::IAM::InstanceProfile", Identifier: "fabrica-perforce-profile"},
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
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	st.UpsertModule("perforce", "2025.2", "provisioning", []fabricastate.ModuleResource{
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
		"fabrica perforce destroy",
	} {
		assert.Contains(t, err.Error(), want)
	}
	if provider.CreateCalls != 0 {
		t.Fatalf("partial module: made %d create calls, want 0", provider.CreateCalls)
	}
}

// TestCreateWriteStateError verifies that a writeState failure after SG is surfaced.
func TestCreateWriteStateError(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = testutil.StateWriteAlwaysError()

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when writeState fails")
	}
	assert.Contains(t, err.Error(), "writing state")
}

// TestCreateAMIRecordedInState verifies the resolved AMI ID is recorded in
// instance Properties (not the version string), so drift and export work correctly.
func TestCreateAMIRecordedInState(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{}
	st := testutil.NewTestState()
	capture := &testutil.StateWriteCapture{}
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.version = "2024.2"
	c.writeState = capture.WriteFunc()

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	final := capture.Last()
	m := final.GetModule("perforce")
	if m == nil {
		t.Fatal("perforce module not in state")
		return
	}

	// ModuleState.Version should be the Helix version string (human-readable).
	if m.Version != "2024.2" {
		t.Errorf("ModuleState.Version = %q, want 2024.2", m.Version)
	}

	// Instance Properties.imageId should be the resolved AMI ID.
	inst, ok := final.GetModuleResource("perforce", "AWS::EC2::Instance")
	if !ok {
		t.Fatal("instance resource missing from state")
	}
	if !strings.HasPrefix(inst.Properties["imageId"], "ami-") {
		t.Errorf("Properties.imageId = %q, want ami-*", inst.Properties["imageId"])
	}
	// Must NOT be the version string.
	if inst.Properties["imageId"] == "2024.2" {
		t.Error("Properties.imageId must not be the version string")
	}
	if inst.Properties["rootDevice"] != "/dev/sda1" {
		t.Errorf("Properties.rootDevice = %q, want the AMI's root device /dev/sda1", inst.Properties["rootDevice"])
	}
}

// #513: the encrypted root mapping lands on the AMI's real root device, so a
// provider that cannot inspect AMIs refuses the launch rather than guessing
// a device name.

func TestCreateRefusesWithoutImageInspector(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refusing to launch") {
		t.Fatalf("expected a refuse-to-launch error, got %v", err)
	}
	if !strings.Contains(err.Error(), "cannot inspect AMIs") {
		t.Errorf("err = %v, want the inspector boundary named", err)
	}
	// The refuse lands after SG/role/profile creation: three creates, no instance.
	if provider.CreateCalls != 3 {
		t.Errorf("CreateCalls = %d, want 3 (SG, role, profile)", provider.CreateCalls)
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
			t.Chdir(t.TempDir())
			var out bytes.Buffer
			c := newTestCommand(&out, &testutil.TestProvider{}, testutil.NewTestState())
			c.assumeYes = true
			c.inspectImage = func(_ context.Context, _ string) (cloud.ImageInfo, error) {
				return cloud.ImageInfo{RootDeviceName: tc.rootDev, RootDeviceType: tc.devType}, nil
			}
			err := c.run(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, err)
			}
		})
	}
}

func TestCreateInspectAMIErrorsWrap(t *testing.T) {
	t.Chdir(t.TempDir())
	c := newTestCommand(&bytes.Buffer{}, &testutil.TestProvider{}, testutil.NewTestState())
	c.assumeYes = true
	c.inspectImage = func(_ context.Context, _ string) (cloud.ImageInfo, error) {
		return cloud.ImageInfo{}, errors.New("describe denied")
	}
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "inspecting perforce AMI ami-fake-ubuntu") {
		t.Fatalf("expected the inspect error, got %v", err)
	}
	if !strings.Contains(err.Error(), "describe denied") {
		t.Errorf("err = %v, want the wrapped cause", err)
	}
}

func TestCreateEmptyAMIResolveRefuses(t *testing.T) {
	t.Chdir(t.TempDir())
	c := newTestCommand(&bytes.Buffer{}, &testutil.InspectorProvider{}, testutil.NewTestState())
	c.assumeYes = true
	c.resolveAMI = nil // the provider is not an AMI resolver: no AMI in the region
	err := c.run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "no Ubuntu AMI could be resolved in us-east-1") {
		t.Fatalf("expected an empty-AMI error, got %v", err)
	}
}

// TestCreateInspectsTheResolvedAMI pins that the inspection targets the
// resolved Ubuntu AMI, not a canned or guessed ID.
func TestCreateInspectsTheResolvedAMI(t *testing.T) {
	t.Chdir(t.TempDir())
	provider := &testutil.InspectorProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&bytes.Buffer{}, provider, st)
	c.assumeYes = true
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.LastImageID != "ami-fake-ubuntu" {
		t.Errorf("LastImageID = %q, want the resolved AMI", provider.LastImageID)
	}
	if len(provider.DescribeImageIDs) != 1 || provider.DescribeImageIDs[0] != "ami-fake-ubuntu" {
		t.Errorf("DescribeImageIDs = %v, want one call for the resolved AMI", provider.DescribeImageIDs)
	}
}
