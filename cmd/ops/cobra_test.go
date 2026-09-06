package ops_test

import (
	"bytes"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/cmd/ops"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

func buildTestRoot(src globals.RuntimeSource, out *bytes.Buffer) *cobra.Command {
	root, optionsSource := testutil.BuildTestSubcommand(out)
	root.AddCommand(ops.New(src, optionsSource, out))
	return root
}

func TestOpsExportCobraDryRun(t *testing.T) {
	cfg := config.Defaults()
	cfg.Ops.Enabled = true
	src := func() (globals.Runtime, error) {
		return globals.Runtime{Config: cfg}, nil
	}
	var out bytes.Buffer
	root := buildTestRoot(src, &out)
	got, err := testutil.RunCommandWithOut(t, root, &out, "ops", "export", "--dry-run")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	testutil.AssertContains(t, got, "logGroup")
}

func TestOpsExportCobraDisabled(t *testing.T) {
	src := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults()}, nil
	}
	var out bytes.Buffer
	root := buildTestRoot(src, &out)
	_, err := testutil.RunCommandWithOut(t, root, &out, "ops", "export")
	if err == nil {
		t.Fatal("expected disabled error")
	}
}
