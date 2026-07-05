package create

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
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
	c.writeState = func(_ *fabricastate.State) error { return nil }
	if provider != nil {
		c.createResource = provider.Resources().Create
	}
	return c
}

func TestCreateDryRunNoAWSCalls(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, provider, st)
	c.dryRun = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.createCalls != 0 {
		t.Fatalf("dry-run made %d create calls, want 0", provider.createCalls)
	}
}

func TestCreateDryRunOutputFields(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
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
		assertContains(t, got, want)
	}
}

func TestCreateAlreadyExists(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule(moduleName, "1", "provisioning", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-existing"},
		{TypeName: "AWS::EC2::Instance", Identifier: "i-existing"},
	})
	c := newTestCommand(&out, provider, st)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.createCalls != 0 {
		t.Fatalf("already-exists: made %d create calls, want 0", provider.createCalls)
	}
	assertContains(t, out.String(), "already provisioned")
}

func TestCreateHappyPathOrderAndState(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	var writtenStates []*fabricastate.State
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = func(s *fabricastate.State) error {
		cp := *s
		writtenStates = append(writtenStates, &cp)
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.createCalls != 2 {
		t.Fatalf("expected 2 create calls, got %d", provider.createCalls)
	}
	if provider.createdTypes[0] != "AWS::EC2::SecurityGroup" {
		t.Errorf("first resource = %q, want AWS::EC2::SecurityGroup", provider.createdTypes[0])
	}
	if provider.createdTypes[1] != "AWS::EC2::Instance" {
		t.Errorf("second resource = %q, want AWS::EC2::Instance", provider.createdTypes[1])
	}
	if len(writtenStates) < 2 {
		t.Fatalf("expected >=2 state writes, got %d", len(writtenStates))
	}
	final := writtenStates[len(writtenStates)-1]
	m := final.GetModule(moduleName)
	if m == nil {
		t.Fatal("workstation module not in final state")
	}
	if len(m.Resources) != 2 {
		t.Fatalf("final state has %d resources, want 2", len(m.Resources))
	}
}

func TestCreateSGFailureNoStateWritten(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{sgCreateErr: errors.New("sg quota")}
	st := fabricastate.NewState("123456789012", "us-east-1")
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
	assertContains(t, err.Error(), "creating security group")
	if stateWritten {
		t.Error("state must not be written when SG creation fails")
	}
}

func TestCreateInstanceFailurePreservesPartialState(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{instanceCreateErr: errors.New("quota exceeded")}
	st := fabricastate.NewState("123456789012", "us-east-1")
	var lastState *fabricastate.State
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = func(s *fabricastate.State) error {
		cp := *s
		lastState = &cp
		return nil
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error on instance create failure")
	}
	assertContains(t, err.Error(), "creating EC2 instance")
	if lastState == nil {
		t.Fatal("state was never written")
	}
	_, hasSG := lastState.GetModuleResource(moduleName, "AWS::EC2::SecurityGroup")
	if !hasSG {
		t.Error("SG resource not recorded in state after instance failure")
	}
}

func TestCreateConfirmationRejected(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, provider, st)
	c.confirm = func(_, _ string) bool { return false }

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.createCalls != 0 {
		t.Fatalf("cancelled: made %d create calls, want 0", provider.createCalls)
	}
	assertContains(t, out.String(), "Cancelled")
}

func TestCreateNilProviderReturnsError(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Defaults()
	c := command{
		runtime: globals.Runtime{Config: cfg, Provider: nil},
		costs:   fabricacost.Global,
		out:     &out,
	}
	c.readState = func() (*fabricastate.State, error) { return fabricastate.NewState("", ""), nil }
	c.writeState = func(_ *fabricastate.State) error { return nil }

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when provider is nil")
	}
	assertContains(t, err.Error(), "no provider configured")
}

func TestCreateIdentityFailure(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{identityErr: errors.New("credentials unavailable")}
	st := fabricastate.NewState("", "")
	c := newTestCommand(&out, provider, st)

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when identity fails")
	}
	if provider.createCalls != 0 {
		t.Fatal("identity failure: create was called")
	}
}

func TestCreateInstanceTypeFlagOverridesConfig(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.instanceType = "g5.2xlarge"
	c.runtime.Config.Workstation.InstanceType = "g4dn.xlarge"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "g5.2xlarge")
}

func TestCreateReadStateError(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	c := newTestCommand(&out, provider, nil)
	c.readState = func() (*fabricastate.State, error) {
		return nil, errors.New("disk read failure")
	}

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when readState fails")
	}
	if provider.createCalls != 0 {
		t.Fatal("readState failure: create was called")
	}
	assertContains(t, err.Error(), "reading state")
}

func TestCreateWriteStateErrorAfterSG(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	callCount := 0
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = func(_ *fabricastate.State) error {
		callCount++
		if callCount == 1 {
			return errors.New("S3 write failed")
		}
		return nil
	}

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when writeState fails after SG")
	}
	if provider.createCalls != 1 {
		t.Fatalf("expected 1 create call (SG only), got %d", provider.createCalls)
	}
	assertContains(t, err.Error(), "writing state after SG creation")
}

type fakeProvider struct {
	identityErr       error
	sgCreateErr       error
	instanceCreateErr error
	createCalls       int
	createdTypes      []string
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Identity(_ context.Context) (string, string, string, error) {
	if f.identityErr != nil {
		return "", "", "", f.identityErr
	}
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}
func (f *fakeProvider) Resources() cloud.ResourceClient {
	return &fakeResourceClient{provider: f}
}

type fakeResourceClient struct{ provider *fakeProvider }

func (r *fakeResourceClient) Create(_ context.Context, res *cloud.Resource) error {
	r.provider.createCalls++
	r.provider.createdTypes = append(r.provider.createdTypes, res.TypeName)
	if res.TypeName == "AWS::EC2::SecurityGroup" && r.provider.sgCreateErr != nil {
		return r.provider.sgCreateErr
	}
	if res.TypeName == "AWS::EC2::Instance" && r.provider.instanceCreateErr != nil {
		return r.provider.instanceCreateErr
	}
	switch res.TypeName {
	case "AWS::EC2::SecurityGroup":
		res.Identifier = fmt.Sprintf("sg-fake%04d", r.provider.createCalls)
	case "AWS::EC2::Instance":
		res.Identifier = fmt.Sprintf("i-fake%04d", r.provider.createCalls)
	}
	return nil
}
func (r *fakeResourceClient) Get(_ context.Context, _ *cloud.Resource) error    { return nil }
func (r *fakeResourceClient) Update(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *fakeResourceClient) Delete(_ context.Context, _ *cloud.Resource) error { return nil }
func (r *fakeResourceClient) List(_ context.Context, _ string) ([]cloud.Resource, error) {
	return nil, nil
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Fatalf("%q does not contain %q", s, substr)
}

// stateWithPerforce returns state with a provisioned Perforce module (SG +
// instance), for --mount-perforce address resolution tests.
func stateWithPerforce() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
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
		return fabricastate.NewState("123456789012", "us-east-1"), nil
	}
	_, err := c.resolvePerforceAddr(context.Background())
	if err == nil {
		t.Fatal("expected error when Perforce module is absent")
	}
	assertContains(t, err.Error(), "fabrica perforce create")
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
