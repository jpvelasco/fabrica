package setup_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/ddc/setup"
	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestCobraDryRun(t *testing.T) {
	var buf bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&buf)
	rt := globals.Runtime{
		Config:   &config.Config{DDC: config.DDCConfig{AmiID: "ami-x", VPCId: "v", SubnetId: "s"}},
		Provider: &testutil.TestProvider{},
	}
	root.AddCommand(setup.New(func() (globals.Runtime, error) { return rt, nil }, optionsSource, &buf))
	root.SetArgs([]string{"setup", "--dry-run"})
	root.SetOut(io.Discard)
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
}
