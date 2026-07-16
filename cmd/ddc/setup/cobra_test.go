package setup_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ddc/setup"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
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
	root := &cobra.Command{Use: "fabrica"}
	var dry bool
	root.PersistentFlags().BoolVarP(&dry, "dry-run", "d", false, "")
	root.PersistentFlags().BoolVarP(new(bool), "yes", "y", false, "")
	root.PersistentFlags().BoolVarP(new(bool), "json", "j", false, "")
	rt := globals.Runtime{
		Config:   &config.Config{DDC: config.DDCConfig{AmiID: "ami-x", VPCId: "v", SubnetId: "s"}},
		Provider: fp{},
	}
	root.AddCommand(setup.New(func() (globals.Runtime, error) { return rt, nil }, func() globals.Options {
		return globals.Options{DryRun: dry}
	}, &buf))
	root.SetArgs([]string{"setup", "--dry-run"})
	root.SetOut(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}
