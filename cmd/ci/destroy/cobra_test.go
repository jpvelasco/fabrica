package destroy_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ci/destroy"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func TestCIDestroyWiring(t *testing.T) {
	t.Chdir(t.TempDir()) // isolate from any real .fabrica/state.json
	var opts globals.Options
	root := &cobra.Command{Use: "fabrica", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().BoolVarP(&opts.JSONOutput, "json", "j", false, "")
	root.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "")
	root.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	src := func() (globals.Runtime, error) { return globals.Runtime{Config: config.Defaults()}, nil }
	optionsSource := func() globals.Options { return opts }
	root.AddCommand(destroy.New(src, optionsSource, &out))
	root.SetArgs([]string{"destroy", "--dry-run"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
}
