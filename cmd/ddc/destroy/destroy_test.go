package destroy

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/ddc"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestResourceOrder(t *testing.T) {
	m := &fabricastate.ModuleState{
		Resources: []fabricastate.ModuleResource{
			{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-1"},
			{TypeName: ddc.TypeAWSIAMRole, Identifier: "role"},
			{TypeName: ddc.TypeAWSIAMInstanceProfile, Identifier: "prof"},
			{TypeName: ddc.TypeAWSS3Bucket, Identifier: "bucket"},
			{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-scylla", Properties: map[string]string{"role": ddc.RoleScylla}},
			{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-ddc", Properties: map[string]string{"role": ddc.RoleCoordinator}},
		},
	}
	got := ResourceOrder(m)
	if len(got) != 6 {
		t.Fatalf("len = %d", len(got))
	}
	if got[0].Identifier != "i-ddc" {
		t.Fatalf("first = %s, want coordinator", got[0].Identifier)
	}
	if got[1].Identifier != "i-scylla" {
		t.Fatalf("second = %s, want scylla", got[1].Identifier)
	}
	if got[2].Identifier != "bucket" {
		t.Fatalf("third = %s", got[2].Identifier)
	}
}

type delFake struct {
	deleted []string
}

func (d *delFake) Name() string { return "fake" }
func (d *delFake) Identity(ctx context.Context) (string, string, string, error) {
	return "1", "a", "us-east-1", nil
}
func (d *delFake) Resources() cloud.ResourceClient                     { return d }
func (d *delFake) Create(ctx context.Context, r *cloud.Resource) error { return nil }
func (d *delFake) Get(ctx context.Context, r *cloud.Resource) error    { return nil }
func (d *delFake) Update(ctx context.Context, r *cloud.Resource) error { return nil }
func (d *delFake) Delete(ctx context.Context, r *cloud.Resource) error {
	d.deleted = append(d.deleted, r.Identifier)
	return nil
}
func (d *delFake) List(ctx context.Context, typeName string) ([]cloud.Resource, error) {
	return nil, nil
}

func TestNewTeardownRun(t *testing.T) {
	fp := &delFake{}
	st := &fabricastate.State{Account: "123"}
	st.UpsertModule("ddc", "ami", "ready", []fabricastate.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-1", Properties: map[string]string{"role": ddc.RoleCoordinator}},
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-1"},
	})
	rt := globals.Runtime{Config: &config.Config{}, Provider: fp}
	var buf bytes.Buffer
	tc := NewTeardown(rt, &buf)
	tc.ReadState = func() (*fabricastate.State, error) { return st, nil }
	tc.WriteState = func(*fabricastate.State) error { return nil }
	tc.DeleteResource = wrapDelete(fp.Delete)
	tc.GetResource = fp.Get
	if err := tc.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fp.deleted) == 0 {
		t.Fatal("expected deletes")
	}
}

func TestNewCobra(t *testing.T) {
	cmd := New(func() (globals.Runtime, error) {
		return globals.Runtime{Config: &config.Config{}}, nil
	}, func() globals.Options { return globals.Options{DryRun: true} }, io.Discard)
	if cmd.Use != "destroy" {
		t.Fatalf("Use = %s", cmd.Use)
	}
}
