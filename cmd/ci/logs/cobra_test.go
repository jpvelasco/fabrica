package logs_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ci/logs"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

type cobraProvider struct{}

func (cobraProvider) Name() string { return "fake" }
func (cobraProvider) Identity(context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-west-2", nil
}
func (cobraProvider) Resources() cloud.ResourceClient { return nil }

// TestLogsCobraWiring exercises New(): a provider without CodeBuildRunner must
// produce a clean error (not a panic) through the full Cobra execution path.
func TestLogsCobraWiring(t *testing.T) {
	var out bytes.Buffer
	root := &cobra.Command{Use: "fabrica", SilenceUsage: true, SilenceErrors: true}
	root.SetOut(&out)
	root.SetErr(&out)
	src := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults(), Provider: cobraProvider{}}, nil
	}
	root.AddCommand(logs.New(src, func() globals.Options { return globals.Options{} }, &out))
	root.SetArgs([]string{"logs", "build-1"})

	if err := root.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected error: provider lacks CodeBuildRunner")
	}
}

// TestLogsRequiresBuildID verifies the ExactArgs(1) constraint.
func TestLogsRequiresBuildID(t *testing.T) {
	var out bytes.Buffer
	root := &cobra.Command{Use: "fabrica", SilenceUsage: true, SilenceErrors: true}
	root.SetOut(&out)
	root.SetErr(&out)
	src := func() (globals.Runtime, error) { return globals.Runtime{Config: config.Defaults()}, nil }
	root.AddCommand(logs.New(src, func() globals.Options { return globals.Options{} }, &out))
	root.SetArgs([]string{"logs"})

	if err := root.ExecuteContext(context.Background()); err == nil {
		t.Fatal("expected error: build-id argument required")
	}
}
