package region

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/ddc"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func testRuntime() globals.Runtime {
	return globals.Runtime{
		Config: &config.Config{
			DDC: config.DDCConfig{
				AmiID:    "ami-ddc",
				VPCId:    "vpc-1",
				SubnetId: "subnet-1",
			},
		},
		Provider: &testutil.TestProvider{},
	}
}

// provisionedState returns a state with a fully provisioned home DDC module.
func provisionedState() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("ddc", "ami-ddc", "ready", []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coord"},
		{TypeName: cloud.TypeAWSS3Bucket, Identifier: "b-home"},
		{TypeName: cloud.TypeAWSIAMInstanceProfile, Identifier: "p-home"},
		{TypeName: cloud.TypeAWSIAMRole, Identifier: "r-home"},
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-home"},
	})
	return st
}

func testCommand(buf *bytes.Buffer, st *fabricastate.State, fp *testutil.TestProvider) command {
	if st == nil {
		st = fabricastate.NewState("123456789012", "us-east-1")
	}
	rt := testRuntime()
	if fp != nil {
		rt.Provider = fp
	}
	return command{
		runtime:   rt,
		assumeYes: true,
		out:       buf,
		costs:     fabricacost.Global,
		confirm:   func(string) bool { return true },
		readState: func() (*fabricastate.State, error) { return st, nil },
		writeState: func(s *fabricastate.State) error {
			st.UpsertModule(s.GetModule("ddc").Name, s.GetModule("ddc").Version, s.GetModule("ddc").Status, s.GetModule("ddc").Resources)
			return nil
		},
	}
}

func TestRunDryRun(t *testing.T) {
	var buf bytes.Buffer
	fp := &testutil.TestProvider{}
	c := testCommand(&buf, nil, fp)
	c.dryRun = true
	if err := c.run(context.Background(), "eu-west-1"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"dry run", "eu-west-1", "fabrica-ddc-edge-eu-west-1", "shared with home"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if fp.CreateCalls != 0 {
		t.Fatalf("dry-run made %d creates", fp.CreateCalls)
	}
}

func TestRunAdd(t *testing.T) {
	var buf bytes.Buffer
	st := provisionedState()
	fp := &testutil.TestProvider{}
	c := testCommand(&buf, st, fp)
	if err := c.run(context.Background(), "eu-west-1"); err != nil {
		t.Fatal(err)
	}
	if fp.CreateCalls != 2 {
		t.Fatalf("creates = %d, want 2 (SG + instance)", fp.CreateCalls)
	}
	m := st.GetModule("ddc")
	var edgeSG, edgeInstance bool
	for _, r := range m.Resources {
		if r.TypeName == cloud.TypeAWSEC2SecurityGroup && r.Properties["region"] == "eu-west-1" && r.Properties["role"] == ddc.RoleEdge {
			edgeSG = true
		}
		if r.TypeName == cloud.TypeAWSEC2Instance && r.Properties["region"] == "eu-west-1" && r.Properties["role"] == ddc.RoleEdge {
			edgeInstance = true
		}
	}
	if !edgeSG || !edgeInstance {
		t.Fatalf("edge resources missing: %+v", m.Resources)
	}
	if !strings.Contains(buf.String(), "provisioned") {
		t.Fatalf("%s", buf.String())
	}
}

func TestRunAlreadyProvisioned(t *testing.T) {
	var buf bytes.Buffer
	st := provisionedState()
	st.UpsertModule("ddc", "ami-ddc", "ready", append(st.GetModule("ddc").Resources,
		fabricastate.ModuleResource{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-edge", Properties: map[string]string{"region": "eu-west-1", "role": ddc.RoleEdge}},
	))
	fp := &testutil.TestProvider{}
	c := testCommand(&buf, st, fp)
	if err := c.run(context.Background(), "eu-west-1"); err != nil {
		t.Fatal(err)
	}
	if fp.CreateCalls != 0 {
		t.Fatalf("creates = %d, want 0", fp.CreateCalls)
	}
	if !strings.Contains(buf.String(), "already provisioned") {
		t.Fatalf("%s", buf.String())
	}
}

func TestRunResumeAfterSG(t *testing.T) {
	var buf bytes.Buffer
	st := provisionedState()
	st.UpsertModule("ddc", "ami-ddc", "ready", append(st.GetModule("ddc").Resources,
		fabricastate.ModuleResource{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-edge", Properties: map[string]string{"region": "eu-west-1", "role": ddc.RoleEdge}},
	))
	fp := &testutil.TestProvider{}
	c := testCommand(&buf, st, fp)
	if err := c.run(context.Background(), "eu-west-1"); err != nil {
		t.Fatal(err)
	}
	if fp.CreateCalls != 1 {
		t.Fatalf("creates = %d, want 1 (instance only; SG reused)", fp.CreateCalls)
	}
	if !strings.Contains(buf.String(), "resuming") {
		t.Fatalf("%s", buf.String())
	}
}

func TestRunNotProvisioned(t *testing.T) {
	var buf bytes.Buffer
	c := testCommand(&buf, nil, nil)
	if err := c.run(context.Background(), "eu-west-1"); err == nil {
		t.Fatal("expected error when DDC module absent")
	}
}

func TestRunConfirmReject(t *testing.T) {
	var buf bytes.Buffer
	st := provisionedState()
	fp := &testutil.TestProvider{}
	c := testCommand(&buf, st, fp)
	c.assumeYes = false
	c.confirm = func(string) bool { return false }
	if err := c.run(context.Background(), "eu-west-1"); err != nil {
		t.Fatal(err)
	}
	if fp.CreateCalls != 0 {
		t.Fatalf("creates = %d, want 0 after reject", fp.CreateCalls)
	}
}

// legacyProvider implements cloud.Provider but not cloud.RegionProvider,
// simulating an older or minimal provider that cannot host edge regions.
type legacyProvider struct{ inner *testutil.TestProvider }

func (l legacyProvider) Name() string { return l.inner.Name() }
func (l legacyProvider) Identity(ctx context.Context) (string, string, string, error) {
	return l.inner.Identity(ctx)
}
func (l legacyProvider) Resources() cloud.ResourceClient { return l.inner.Resources() }

func TestRunNoRegionProvider(t *testing.T) {
	var buf bytes.Buffer
	st := provisionedState()
	c := testCommand(&buf, st, nil)
	c.runtime.Provider = legacyProvider{inner: &testutil.TestProvider{}}
	c.dryRun = true
	if err := c.run(context.Background(), "eu-west-1"); err == nil {
		t.Fatal("expected error when provider lacks RegionProvider")
	}
}

func TestRunInvalidRegion(t *testing.T) {
	var buf bytes.Buffer
	st := provisionedState()
	fp := &testutil.TestProvider{}
	c := testCommand(&buf, st, fp)
	c.dryRun = true
	if err := c.run(context.Background(), "us-east-1"); err == nil {
		t.Fatal("expected error for home region as edge region")
	}
}

func TestRunIdentityError(t *testing.T) {
	var buf bytes.Buffer
	fp := &testutil.TestProvider{IdentityErr: errors.New("creds expired")}
	c := testCommand(&buf, nil, fp)
	if err := c.run(context.Background(), "eu-west-1"); err == nil {
		t.Fatal("expected identity error")
	}
}

func TestRunStateReadError(t *testing.T) {
	var buf bytes.Buffer
	fp := &testutil.TestProvider{}
	c := testCommand(&buf, nil, fp)
	c.dryRun = false
	c.readState = func() (*fabricastate.State, error) { return nil, errors.New("state read failed") }
	if err := c.run(context.Background(), "eu-west-1"); err == nil {
		t.Fatal("expected state read error")
	}
}

func TestRunCreateError(t *testing.T) {
	var buf bytes.Buffer
	st := provisionedState()
	fp := &testutil.TestProvider{CreateErr: map[string]error{cloud.TypeAWSEC2SecurityGroup: errors.New("sg boom")}}
	c := testCommand(&buf, st, fp)
	if err := c.run(context.Background(), "eu-west-1"); err == nil {
		t.Fatal("expected SG create error")
	}
	if m := st.GetModule("ddc"); m != nil {
		for _, r := range m.Resources {
			if r.Properties != nil && r.Properties["region"] == "eu-west-1" {
				t.Fatalf("no edge resources may be recorded on failure, got %+v", m.Resources)
			}
		}
	}
}

func TestRunInstanceCreateError(t *testing.T) {
	var buf bytes.Buffer
	st := provisionedState()
	fp := &testutil.TestProvider{CreateErr: map[string]error{cloud.TypeAWSEC2Instance: errors.New("instance boom")}}
	c := testCommand(&buf, st, fp)
	err := c.run(context.Background(), "eu-west-1")
	if err == nil || !strings.Contains(err.Error(), "instance boom") {
		t.Fatalf("err = %v, want instance create failure", err)
	}
	// SG must be recorded so a re-run resumes instead of duplicating.
	m := st.GetModule("ddc")
	found := false
	for _, r := range m.Resources {
		if r.TypeName == cloud.TypeAWSEC2SecurityGroup && r.Properties["region"] == "eu-west-1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("edge SG not persisted for resume: %+v", m.Resources)
	}
}

func TestRunStateWriteErrorAfterSG(t *testing.T) {
	var buf bytes.Buffer
	st := provisionedState()
	fp := &testutil.TestProvider{}
	c := testCommand(&buf, st, fp)
	c.writeState = func(*fabricastate.State) error { return errors.New("disk full") }
	err := c.run(context.Background(), "eu-west-1")
	if err == nil || !strings.Contains(err.Error(), "writing state after edge security group") {
		t.Fatalf("err = %v, want SG state-write failure", err)
	}
}

func TestRunStateWriteErrorAfterInstance(t *testing.T) {
	var buf bytes.Buffer
	st := provisionedState()
	fp := &testutil.TestProvider{}
	c := testCommand(&buf, st, fp)
	writes := 0
	c.writeState = func(*fabricastate.State) error {
		writes++
		if writes == 2 {
			return errors.New("disk full")
		}
		return nil
	}
	err := c.run(context.Background(), "eu-west-1")
	if err == nil || !strings.Contains(err.Error(), "writing state after edge instance") {
		t.Fatalf("err = %v, want instance state-write failure", err)
	}
}
