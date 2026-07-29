package cost_test

import (
	"bytes"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/cost"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(src globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
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
	got, err := testutil.RunCommandWithOut(t, root, &out, "cost", "report")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	testutil.AssertContains(t, got, "Cost estimate")
}
