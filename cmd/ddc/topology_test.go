package ddc

import (
	"bytes"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestTopologyCobra(t *testing.T) {
	cfg := config.Defaults()
	cfg.DDC.Backend = "scylla"
	cfg.DDC.Scylla.Nodes = 3
	src := func() (globals.Runtime, error) { return globals.Runtime{Config: cfg}, nil }
	var out bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&out)
	root.AddCommand(New(src, optionsSource, &out))
	got, err := testutil.RunCommandWithOut(t, root, &out, "ddc", "topology")
	if err != nil {
		t.Fatalf("topology: %v", err)
	}
	testutil.AssertContains(t, got, "RF=3")
}

func TestTopologyCobraInvalid(t *testing.T) {
	cfg := config.Defaults()
	cfg.DDC.Backend = "scylla"
	cfg.DDC.Scylla.Nodes = 2
	src := func() (globals.Runtime, error) { return globals.Runtime{Config: cfg}, nil }
	var out bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&out)
	root.AddCommand(New(src, optionsSource, &out))
	if _, err := testutil.RunCommandWithOut(t, root, &out, "ddc", "topology"); err == nil {
		t.Fatal("expected invalid topology")
	}
}
