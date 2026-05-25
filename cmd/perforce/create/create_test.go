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
	c.writeState = func(_ *fabricastate.State) error { return nil }
	if provider != nil {
		c.createResource = provider.Resources().Create
	}
	return c
}

// TestCreateDryRunNoAWSCalls verifies --dry-run makes zero provider calls.
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

// TestCreateDryRunOutputFields verifies key fields appear in dry-run output.
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
		"fabrica-perforce-sg",
		"fabrica-perforce",
		"Cost estimate:",
	} {
		assertContains(t, got, want)
	}
}

// TestCreateAlreadyExists verifies clean exit when module is already in state.
func TestCreateAlreadyExists(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("perforce", "2024.2", "provisioning", []fabricastate.ModuleResource{
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

// TestCreateIdentityFailureAbortsEarly verifies no AWS calls on identity error.
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

// TestCreateHappyPathOrderAndState verifies SG created before instance, both in state.
func TestCreateHappyPathOrderAndState(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	var writtenStates []*fabricastate.State
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = func(s *fabricastate.State) error {
		// Deep copy resources for inspection
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

	// First type created must be the security group
	if provider.createdTypes[0] != "AWS::EC2::SecurityGroup" {
		t.Errorf("first created resource = %q, want AWS::EC2::SecurityGroup", provider.createdTypes[0])
	}
	if provider.createdTypes[1] != "AWS::EC2::Instance" {
		t.Errorf("second created resource = %q, want AWS::EC2::Instance", provider.createdTypes[1])
	}

	// State must have been written at least twice (after SG, after instance)
	if len(writtenStates) < 2 {
		t.Fatalf("expected >=2 state writes, got %d", len(writtenStates))
	}

	// Final state must have both resources
	final := writtenStates[len(writtenStates)-1]
	m := final.GetModule("perforce")
	if m == nil {
		t.Fatal("perforce module not in final state")
	}
	if len(m.Resources) != 2 {
		t.Fatalf("final state has %d resources, want 2", len(m.Resources))
	}
}

// TestCreateInstanceFailurePreservesPartialState verifies SG is in state even on instance error.
func TestCreateInstanceFailurePreservesPartialState(t *testing.T) {
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

	// SG identifier must be recorded even though instance failed
	if lastWrittenState == nil {
		t.Fatal("state was never written")
	}
	_, hasSG := lastWrittenState.GetModuleResource("perforce", "AWS::EC2::SecurityGroup")
	if !hasSG {
		t.Error("SG resource not recorded in state after instance failure")
	}
}

// TestCreateConfirmationRejectedNoAWSCalls verifies cancellation skips create.
func TestCreateConfirmationRejectedNoAWSCalls(t *testing.T) {
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

// TestCreateNilProviderReturnsError verifies nil provider returns a clear error.
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
	assertContains(t, err.Error(), "fabrica setup")
}

// TestCreateVersionFlagInvalidAbortsBeforeAWS verifies bad version errors early.
func TestCreateVersionFlagInvalidAbortsBeforeAWS(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, provider, st)
	c.version = "bad-version"

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error for invalid version")
	}
	if provider.createCalls != 0 {
		t.Fatal("invalid version: create was called")
	}
}

// TestCreateReadStateError verifies error is surfaced before any AWS call.
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

// TestCreateSGFailureNoStateWritten verifies state is never written when SG creation fails.
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

// TestCreateDryRunVersionPinned verifies "(pinned)" label appears for non-latest version.
func TestCreateDryRunVersionPinned(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.version = "2024.2"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "(pinned)")
}

// TestCreateDryRunVersionLatestNotPinned verifies "latest" does not get "(pinned)" label.
func TestCreateDryRunVersionLatestNotPinned(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.version = "latest"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := out.String()
	assertContains(t, got, "latest")
	if containsStr(got, "(pinned)") {
		t.Error("'latest' should not show '(pinned)' label")
	}
}

// TestCreateFlagOverridesConfigInstanceType verifies --instance-type flag wins over config.
func TestCreateFlagOverridesConfigInstanceType(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.instanceType = "c5.2xlarge"
	c.runtime.Config.Perforce.InstanceType = "m5.xlarge"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "c5.2xlarge")
}

// TestCreateFlagOverridesConfigVolumeSize verifies --volume-size flag wins over config.
func TestCreateFlagOverridesConfigVolumeSize(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.volumeSize = 1000
	c.runtime.Config.Perforce.VolumeSize = 500

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "1000 GiB")
}

// TestCreateVersionFromConfig verifies version is read from config when flag is empty.
func TestCreateVersionFromConfig(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, provider, st)
	c.dryRun = true
	c.version = ""
	c.runtime.Config.Perforce.Version = "2025.1"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	assertContains(t, out.String(), "2025.1")
}

// TestCreateWriteStateError verifies that a writeState failure after SG is surfaced.
func TestCreateWriteStateError(t *testing.T) {
	var out bytes.Buffer
	provider := &fakeProvider{}
	st := fabricastate.NewState("123456789012", "us-east-1")
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.writeState = func(_ *fabricastate.State) error {
		return errors.New("disk full")
	}

	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when writeState fails")
	}
	assertContains(t, err.Error(), "writing state")
}

func containsStr(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---- fakeProvider ----

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
	// Assign fake identifiers
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
	if len(substr) == 0 {
		return
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return
		}
	}
	t.Fatalf("%q\ndoes not contain\n%q", s, substr)
}
