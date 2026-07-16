package setup

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
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
		Provider: &fakeProvider{},
	}
}

type fakeProvider struct {
	n int
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Identity(ctx context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-east-1", nil
}
func (f *fakeProvider) Resources() cloud.ResourceClient { return f }
func (f *fakeProvider) Create(ctx context.Context, r *cloud.Resource) error {
	f.n++
	r.Identifier = fmt.Sprintf("%s-%d", r.TypeName, f.n)
	return nil
}
func (f *fakeProvider) Get(ctx context.Context, r *cloud.Resource) error    { return nil }
func (f *fakeProvider) Update(ctx context.Context, r *cloud.Resource) error { return nil }
func (f *fakeProvider) Delete(ctx context.Context, r *cloud.Resource) error { return nil }
func (f *fakeProvider) List(ctx context.Context, typeName string) ([]cloud.Resource, error) {
	return nil, nil
}

func TestRunDryRun(t *testing.T) {
	var buf bytes.Buffer
	st := &fabricastate.State{}
	c := command{
		runtime: testRuntime(), dryRun: true, out: &buf, costs: fabricacost.Global,
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "dry run") {
		t.Fatalf("%s", buf.String())
	}
}

func TestRunAlreadyProvisioned(t *testing.T) {
	var buf bytes.Buffer
	st := &fabricastate.State{}
	st.UpsertModule("ddc", "ami", "ready", nil)
	c := command{
		runtime: testRuntime(), assumeYes: true, out: &buf, costs: fabricacost.Global,
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(*fabricastate.State) error { return nil },
		createResource: func(ctx context.Context, r *cloud.Resource) error {
			t.Fatal("should not create")
			return nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "already provisioned") {
		t.Fatalf("%s", buf.String())
	}
}

func TestRunApplyZen(t *testing.T) {
	var buf bytes.Buffer
	st := &fabricastate.State{Account: "123456789012", Region: "us-east-1"}
	fp := &fakeProvider{}
	rt := testRuntime()
	rt.Provider = fp
	var wrote string
	c := command{
		runtime: rt, assumeYes: true, out: &buf, costs: fabricacost.Global,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		writeState:     func(s *fabricastate.State) error { st = s; return nil },
		createResource: fp.Create,
		writeEndpoints: func(path, content string) error { wrote = content; return nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st.GetModule("ddc") == nil {
		t.Fatal("module missing")
	}
	if !strings.Contains(wrote, "backend") {
		t.Fatalf("endpoints: %s", wrote)
	}
	// role, profile, bucket, sg, instance = 5
	if fp.n != 5 {
		t.Fatalf("creates = %d, want 5", fp.n)
	}
}

func TestRunConfirmReject(t *testing.T) {
	var buf bytes.Buffer
	st := &fabricastate.State{}
	c := command{
		runtime: testRuntime(), out: &buf, costs: fabricacost.Global,
		confirm:    func(string) bool { return false },
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(*fabricastate.State) error { return nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "cancelled") && !strings.Contains(buf.String(), "Cancelled") {
		t.Fatalf("%s", buf.String())
	}
}

func TestRunMissingAmi(t *testing.T) {
	rt := testRuntime()
	rt.Config.DDC.AmiID = ""
	c := command{runtime: rt, dryRun: true, out: &bytes.Buffer{}, costs: fabricacost.Global}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}
