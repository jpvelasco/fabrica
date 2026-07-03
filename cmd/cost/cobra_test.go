package cost_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/cost"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(src globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	var opts globals.Options
	root := &cobra.Command{Use: "fabrica", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().BoolVarP(&opts.JSONOutput, "json", "j", false, "")
	root.PersistentFlags().BoolVarP(&opts.DryRun, "dry-run", "d", false, "")
	root.PersistentFlags().BoolVarP(&opts.AssumeYes, "yes", "y", false, "")
	root.SetOut(out)
	root.SetErr(out)
	optionsSource := func() globals.Options { return opts }
	root.AddCommand(cost.New(src, optionsSource, out))
	return root
}

func TestCostReportWiring(t *testing.T) {
	t.Chdir(t.TempDir())
	src := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults()}, nil
	}
	var out bytes.Buffer
	root := buildTestRoot(src, &out)
	root.SetArgs([]string{"cost", "report"})
	if err := root.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.String(), "Cost estimate") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
}
