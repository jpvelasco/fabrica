package ddc_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ddc"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

type fakeProv struct{}

func (fakeProv) Name() string { return "fake" }
func (fakeProv) Identity(ctx context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-east-1", nil
}
func (fakeProv) Resources() cloud.ResourceClient { return fakeProv{} }
func (fakeProv) Create(ctx context.Context, r *cloud.Resource) error {
	r.Identifier = "id"
	return nil
}
func (fakeProv) Get(ctx context.Context, r *cloud.Resource) error                    { return nil }
func (fakeProv) Update(ctx context.Context, r *cloud.Resource) error                 { return nil }
func (fakeProv) Delete(ctx context.Context, r *cloud.Resource) error                 { return nil }
func (fakeProv) List(ctx context.Context, typeName string) ([]cloud.Resource, error) { return nil, nil }

func TestParentNewWiresSubcommands(t *testing.T) {
	rt := globals.Runtime{
		Config: &config.Config{DDC: config.DDCConfig{
			AmiID: "ami-x", VPCId: "vpc-1", SubnetId: "subnet-1",
		}},
		Provider: fakeProv{},
	}
	var buf bytes.Buffer
	parent := ddc.New(
		func() (globals.Runtime, error) { return rt, nil },
		func() globals.Options { return globals.Options{DryRun: true} },
		&buf,
	)
	if parent.Use != "ddc" {
		t.Fatalf("Use = %q", parent.Use)
	}
	names := map[string]bool{}
	for _, c := range parent.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"setup", "status", "destroy"} {
		if !names[want] {
			t.Fatalf("missing subcommand %q; have %v", want, names)
		}
	}
	// Ensure no multi-region command slipped in.
	if names["region"] {
		t.Fatal("region subcommand must not exist in V1")
	}

	root := &cobra.Command{Use: "fabrica"}
	root.PersistentFlags().BoolP("dry-run", "d", false, "")
	root.PersistentFlags().BoolP("yes", "y", false, "")
	root.PersistentFlags().BoolP("json", "j", false, "")
	root.AddCommand(parent)
	root.SetArgs([]string{"ddc", "setup", "--dry-run"})
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "dry run") && !strings.Contains(buf.String(), "Distributed DDC") {
		// dry-run writes to the out passed to New, which is buf
		if buf.Len() == 0 {
			t.Fatal("expected setup dry-run output via parent New")
		}
	}
}
