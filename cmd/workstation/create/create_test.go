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
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func newTestCommand(out *bytes.Buffer, provider cloud.Provider, st *fabricastate.State) command {
	cfg := config.Defaults()
	cfg.State.Bucket = "fabrica-state-test"
	cfg.Workstation.AmiID = "ami-test12345"
	cfg.Workstation.VPCId = "vpc-test"
	cfg.Workstation.SubnetId = "subnet-test"
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
		"fabrica-workstation-sg",
		"fabrica-workstation",
		"Cost estimate:",
	} {
		assert.Contains(t, got, want)
	}
}

func TestCreateAlreadyExists(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	st.UpsertModule(moduleName, "1", "provisioning", []fabricastate.ModuleResource{
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
	if provider.CreateCalls != 2 {
		t.Fatalf("expected 2 create calls, got %d", provider.CreateCalls)
	}
	if provider.CreatedTypes[0] != "AWS::EC2::SecurityGroup" {
		t.Errorf("first resource = %q, want AWS::EC2::SecurityGroup", provider.CreatedTypes[0])
	}
	if provider.CreatedTypes[1] != "AWS::EC2::Instance" {
		t.Errorf("second resource = %q, want AWS::EC2::Instance", provider.CreatedTypes[1])
	}
	if len(capture.States) < 2 {
		t.Fatalf("expected >=2 state writes, got %d", len(capture.States))
	}
	final := capture.Last()
	m := final.GetModule(moduleName)
	if m == nil {
		t.Fatal("workstation module not in final state")
		return
	}
	if len(m.Resources) != 2 {
		t.Fatalf("final state has %d resources, want 2", len(m.Resources))
	}
}

// TestCreateApplyPlanIsTerser verifies workstation apply (non-dry-run) keeps the
// pre-#162 layout: no Data volume line, compact labels, open-CIDR WARNING before
// the resource list. Dry-run still shows volume via provision.DryRun.
func TestCreateApplyPlanIsTerser(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	// Default allowedCidr is 10.0.0.0/8 — no WARNING should appear.
	c.writeState = testutil.StateWriteNever()

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "Data volume") {
		t.Errorf("apply plan must not print Data volume, got:\n%s", got)
	}
	assert.Contains(t, got, "  AWS account:   ")
	assert.Contains(t, got, "  Instance type: ")
	// Default CIDR is private; no WARNING should appear.
	if strings.Contains(got, "WARNING: allowedCidr is 0.0.0.0/0") {
		t.Error("CIDR WARNING must not appear with default private CIDR")
	}
	assert.Contains(t, got, "  Security Group: ")
}

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

func TestCreateInstanceFailurePreservesPartialState(t *testing.T) {
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
	_, hasSG := capture.Last().GetModuleResource(moduleName, "AWS::EC2::SecurityGroup")
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

func TestCreateIdentityFailure(t *testing.T) {
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
}

func TestCreateInstanceTypeFlagOverridesConfig(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.instanceType = "g5.2xlarge"
	c.runtime.Config.Workstation.InstanceType = "g4dn.xlarge"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assert.Contains(t, out.String(), "g5.2xlarge")
}

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

func TestCreateWriteStateErrorAfterSG(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = testutil.StateWriteError(1)

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when writeState fails after SG")
	}
	if provider.CreateCalls != 1 {
		t.Fatalf("expected 1 create call (SG only), got %d", provider.CreateCalls)
	}
	assert.Contains(t, err.Error(), "writing state")
}

// stateWithPerforce returns state with a provisioned Perforce module (SG +
// instance), for --mount-perforce address resolution tests.
func stateWithPerforce() *fabricastate.State {
	st := testutil.NewTestState()
	st.UpsertModule("perforce", "2024.2", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-p4"},
		{TypeName: "AWS::EC2::Instance", Identifier: "i-p4server"},
	})
	return st
}

func TestResolvePerforceAddrSuccess(t *testing.T) {
	st := stateWithPerforce()
	c := command{}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = []byte(`{"PrivateIpAddress":"10.0.4.12"}`)
		return nil
	}
	addr, err := c.resolvePerforceAddr(context.Background())
	if err != nil {
		t.Fatalf("resolvePerforceAddr: %v", err)
	}
	if addr != "10.0.4.12:1666" {
		t.Errorf("addr = %q, want 10.0.4.12:1666", addr)
	}
}

func TestResolvePerforceAddrNoModule(t *testing.T) {
	c := command{}
	c.readState = func() (*fabricastate.State, error) {
		return testutil.NewTestState(), nil
	}
	_, err := c.resolvePerforceAddr(context.Background())
	if err == nil {
		t.Fatal("expected error when Perforce module is absent")
	}
	assert.Contains(t, err.Error(), "fabrica perforce create")
}

func TestResolvePerforceAddrNoIP(t *testing.T) {
	st := stateWithPerforce()
	c := command{}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = []byte(`{"PrivateIpAddress":""}`)
		return nil
	}
	_, err := c.resolvePerforceAddr(context.Background())
	if err == nil {
		t.Fatal("expected error when private IP is empty")
	}
}

func TestResolvePerforceAddrReadStateError(t *testing.T) {
	c := command{}
	c.readState = func() (*fabricastate.State, error) {
		return nil, errors.New("state corrupted")
	}
	_, err := c.resolvePerforceAddr(context.Background())
	if err == nil {
		t.Fatal("expected error when readState fails during Perforce address resolution")
	}
	assert.Contains(t, err.Error(), "reading state for Perforce address")
}

func TestResolvePerforceAddrNilGetResource(t *testing.T) {
	st := stateWithPerforce()
	c := command{}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	// getResource left nil
	_, err := c.resolvePerforceAddr(context.Background())
	if err == nil {
		t.Fatal("expected error when getResource seam is nil")
	}
	assert.Contains(t, err.Error(), "no provider configured")
}

func TestResolvePerforceAddrEmptyActualState(t *testing.T) {
	st := stateWithPerforce()
	c := command{}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		// ActualState left empty/nil
		return nil
	}
	_, err := c.resolvePerforceAddr(context.Background())
	if err == nil {
		t.Fatal("expected error when ActualState is empty")
	}
	assert.Contains(t, err.Error(), "no state data")
}

func TestResolvePerforceAddrJSONUnmarshalFailure(t *testing.T) {
	st := stateWithPerforce()
	c := command{}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = []byte("not valid json{")
		return nil
	}
	_, err := c.resolvePerforceAddr(context.Background())
	if err == nil {
		t.Fatal("expected error when ActualState JSON is invalid")
	}
	assert.Contains(t, err.Error(), "could not determine Perforce private IP")
}

func TestResolvePerforceAddrGetResourceError(t *testing.T) {
	st := stateWithPerforce()
	c := command{}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		return errors.New("service unavailable")
	}
	_, err := c.resolvePerforceAddr(context.Background())
	if err == nil {
		t.Fatal("expected error when getResource fails")
	}
	assert.Contains(t, err.Error(), "querying Perforce instance")
	assert.Contains(t, err.Error(), "service unavailable")
}

func TestResolvePerforceAddrNoInstanceInState(t *testing.T) {
	st := testutil.NewTestState()
	st.UpsertModule("perforce", "2024.2", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-p4"},
	})
	c := command{}
	c.readState = func() (*fabricastate.State, error) { return st, nil }
	_, err := c.resolvePerforceAddr(context.Background())
	if err == nil {
		t.Fatal("expected error when Perforce instance not found in state")
	}
	assert.Contains(t, err.Error(), "Perforce instance not found")
}

func TestCreateMountPerforceNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState() // no perforce module
	c := newTestCommand(&out, provider, st)
	c.mountPerforce = true

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when --mount-perforce but Perforce not provisioned")
	}
	assert.Contains(t, err.Error(), "fabrica perforce create")
}

func TestCreateMountPerforceSuccessDryRun(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := stateWithPerforce()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.mountPerforce = true
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = []byte(`{"PrivateIpAddress":"10.0.4.12"}`)
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	assert.Contains(t, got, "Perforce server")
	assert.Contains(t, got, "10.0.4.12:1666")
}

func TestCreateMountPerforceSuccessApply(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := stateWithPerforce()
	capture := &testutil.StateWriteCapture{}
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.mountPerforce = true
	c.writeState = capture.WriteFunc()
	c.getResource = func(_ context.Context, r *cloud.Resource) error {
		r.ActualState = []byte(`{"PrivateIpAddress":"10.0.4.12"}`)
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	assert.Contains(t, got, "Perforce")
	assert.Contains(t, got, "10.0.4.12:1666")
}

func TestCreateVolumeSizeFlagOverridesConfig(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.volumeSize = 200
	c.runtime.Config.Workstation.VolumeSize = 100

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assert.Contains(t, out.String(), "200")
}

func TestCreateSGCreateError(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{CreateErr: map[string]error{cloud.TypeAWSEC2SecurityGroup: errors.New("sg limit")}}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error on SG creation failure")
	}
	assert.Contains(t, err.Error(), "creating security group")
}

func TestCreateCidrWarningInApplyPlan(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = testutil.StateWriteNever()

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	// Default allowedCidr is 10.0.0.0/8 — no WARNING should appear.
	if strings.Contains(got, "WARNING: allowedCidr is 0.0.0.0/0") {
		t.Error("CIDR WARNING must not appear with default private CIDR")
	}
}

func TestCreateCidrNoWarningWhenNotDefault(t *testing.T) {
	var out bytes.Buffer
	provider := &testutil.TestProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = testutil.StateWriteNever()
	c.runtime.Config.Workstation.AllowedCIDR = "10.0.0.0/8"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "WARNING: allowedCidr is 0.0.0.0/0") {
		t.Error("CIDR WARNING must not appear when allowedCidr is not 0.0.0.0/0")
	}
}

func TestNewRuntimeSourceError(t *testing.T) {
	var out bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&out)
	runtimeSource := func() (globals.Runtime, error) {
		return globals.Runtime{}, errors.New("config load failed")
	}
	cmd := New(runtimeSource, optionsSource, &out)
	root.AddCommand(cmd)
	root.SetArgs([]string{"create", "--dry-run"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error when runtimeSource fails")
	}
	assert.Contains(t, err.Error(), "config load failed")
}
