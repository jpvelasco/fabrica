package perforce_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/perforce"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestNewWiresSubcommands(t *testing.T) {
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults()}, nil
	}
	opts := func() globals.Options { return globals.Options{} }
	cmd := perforce.New(rt, opts, io.Discard)
	if cmd.Use != "perforce" {
		t.Fatalf("Use = %q", cmd.Use)
	}
	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"create", "status", "backup", "restore", "destroy"} {
		if !names[want] {
			t.Errorf("missing subcommand %q", want)
		}
	}
	// Execute help path so command tree is fully constructed.
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--help"})
	_ = cmd.Execute()
}
