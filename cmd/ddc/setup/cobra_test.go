package setup_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ddc/setup"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

type fp struct{}

func (fp) Name() string { return "fake" }
func (fp) Identity(ctx context.Context) (string, string, string, error) {
	return "1", "a", "us-east-1", nil
}
func (fp) Resources() cloud.ResourceClient { return fp{} }
func (fp) Create(ctx context.Context, r *cloud.Resource) error {
	r.Identifier = "id"
	return nil
}
func (fp) Get(ctx context.Context, r *cloud.Resource) error                    { return nil }
func (fp) Update(ctx context.Context, r *cloud.Resource) error                 { return nil }
func (fp) Delete(ctx context.Context, r *cloud.Resource) error                 { return nil }
func (fp) List(ctx context.Context, typeName string) ([]cloud.Resource, error) { return nil, nil }

func TestCobraDryRun(t *testing.T) {
	var buf bytes.Buffer
	root, opts := testutil.BuildTestRoot(&buf)
	rt := globals.Runtime{
		Config:   &config.Config{DDC: config.DDCConfig{AmiID: "ami-x", VPCId: "v", SubnetId: "s"}},
		Provider: fp{},
	}
	root.AddCommand(setup.New(func() (globals.Runtime, error) { return rt, nil }, func() globals.Options {
		return *opts
	}, &buf))
	root.SetArgs([]string{"setup", "--dry-run"})
	root.SetOut(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}
