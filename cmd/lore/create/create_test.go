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
		"fabrica-lore-sg",
		"fabrica-lore",
		"41337",
		"41339",
		"Cost estimate:",
	} {
		assertContains(t, got, want)
	}
}

func TestCreateAlreadyProvisioned(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("lore", "ami-existing", "provisioning", []fabricastate.ModuleResource{
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

func TestCreateMissingAmiID(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, provider, st)
	c.runtime.Config.Lore.AmiID = ""

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when AmiID is empty")
	}
	assertContains(t, err.Error(), "lore.amiId is required")
	assertContains(t, err.Error(), "lore-ami.md")
	if provider.createCalls != 0 {
		t.Fatal("missing AmiID: create was called")
	}
}

func TestCreateHappyPathOrderAndState(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	var writtenStates []*fabricastate.State
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = func(s *fabricastate.State) error {
		copy := *s
		writtenStates = append(writtenStates, &copy)
		return nil
	}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if provider.createCalls != 2 {
		t.Fatalf("expected 2 create calls, got %d", provider.createCalls)
	}
	if provider.createdTypes[0] != "AWS::EC2::SecurityGroup" {
		t.Errorf("first created = %q", provider.createdTypes[0])
	}
	if provider.createdTypes[1] != "AWS::EC2::Instance" {
		t.Errorf("second created = %q", provider.createdTypes[1])
	}
	if len(writtenStates) < 2 {
		t.Fatalf("expected >=2 state writes, got %d", len(writtenStates))
	}
	final := writtenStates[len(writtenStates)-1]
	m := final.GetModule("lore")
	if m == nil {
		t.Fatal("lore module not in final state")
	}
	if len(m.Resources) != 2 {
		t.Fatalf("final state has %d resources, want 2", len(m.Resources))
	}
	if m.Version != "ami-test123" {
		t.Errorf("state version = %q, want ami-test123", m.Version)
	}
	assertContains(t, out.String(), "Lore server provisioned")
}

func TestCreateInstanceFailurePreservesPartialState(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	provider := &fakeProvider{instanceCreateErr: errors.New("quota exceeded")}
	st := fabricastate.NewState("123456789012", "us-east-1")
	var lastWrittenState *fabricastate.State
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = func(s *fabricastate.State) error {
		copy := *s
		lastWrittenState = &copy
		return nil
	}

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error on instance create failure")
	}
	assertContains(t, err.Error(), "creating EC2 instance")
	if lastWrittenState == nil {
		t.Fatal("state was never written")
	}
	_, hasSG := lastWrittenState.GetModuleResource("lore", "AWS::EC2::SecurityGroup")
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

func TestCreateAllowedCIDRWarning(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.runtime.Config.Lore.AllowedCIDR = "0.0.0.0/0"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "WARNING")
	assertContains(t, out.String(), "0.0.0.0/0")
}

func TestCreateIdentityFailureAbortsEarly(t *testing.T) {
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
	assertContains(t, err.Error(), "resolving identity")
}

func TestCreateSGFailureNoStateWritten(t *testing.T) {
	t.Chdir(t.TempDir())
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

func TestCreateFlagOverridesConfig(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.instanceType = "m5.2xlarge"
	c.volumeSize = 1000

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "m5.2xlarge")
	assertContains(t, out.String(), "1000 GiB")
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

type fakeResourceClient struct {
	provider *fakeProvider
}

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
	if !bytes.Contains([]byte(s), []byte(substr)) {
		t.Fatalf("%q\ndoes not contain\n%q", s, substr)
	}
}
