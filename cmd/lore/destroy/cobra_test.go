package destroy_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/lore/destroy"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func TestDestroyCobraNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	var opts globals.Options
	root := &cobra.Command{Use: "fabrica", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "")
	root.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "")
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: nil}
	root.AddCommand(destroy.New(
		func() (globals.Runtime, error) { return rt, nil },
		func() globals.Options { return opts },
		&out,
	))
	root.SetArgs([]string{"destroy", "--yes"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if !strings.Contains(out.String(), "not provisioned") {
		t.Fatalf("got %q", out.String())
	}
}

func TestNewTeardownWiring(t *testing.T) {
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg, Provider: nil}
	tc := destroy.NewTeardown(rt, io.Discard)
	if tc.Spec.ModuleName != "lore" {
		t.Errorf("ModuleName = %q", tc.Spec.ModuleName)
	}
	if !tc.SkipConfirm {
		t.Error("SkipConfirm should be true for orchestrated teardown")
	}
}
