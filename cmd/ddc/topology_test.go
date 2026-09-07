package ddc

import (
	"bytes"
	"os"
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

func TestTopologyCobraJSONAndPeers(t *testing.T) {
	cfg := config.Defaults()
	cfg.DDC.Backend = "scylla"
	cfg.DDC.Scylla.Nodes = 3
	cfg.DDC.Scylla.Datacenter = "us-east"
	cfg.DDC.Replication.Enabled = true
	cfg.DDC.Replication.Peers = []string{"10.1.0.8:8080", "10.1.0.9:8080"}
	src := func() (globals.Runtime, error) { return globals.Runtime{Config: cfg}, nil }
	var out bytes.Buffer
	root, _ := testutil.BuildTestSubcommand(&out)
	opts := func() globals.Options { return globals.Options{JSONOutput: true} }
	root.AddCommand(New(src, opts, &out))
	got, err := testutil.RunCommandWithOut(t, root, &out, "ddc", "topology", "--json")
	if err != nil {
		t.Fatalf("json: %v", err)
	}
	testutil.AssertContains(t, got, `"production": true`)
	out.Reset()
	root, optionsSource := testutil.BuildTestSubcommand(&out)
	root.AddCommand(New(src, optionsSource, &out))
	got, err = testutil.RunCommandWithOut(t, root, &out, "ddc", "topology")
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertContains(t, got, "Datacenter:")
	testutil.AssertContains(t, got, "10.1.0.8:8080")
}

func TestTopologyCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) { return globals.Runtime{}, os.ErrNotExist }
	var out bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&out)
	root.AddCommand(New(src, optionsSource, &out))
	if _, err := testutil.RunCommandWithOut(t, root, &out, "ddc", "topology"); err == nil {
		t.Fatal("expected runtime error")
	}
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
