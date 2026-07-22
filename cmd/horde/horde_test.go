package horde

import (
	"bytes"
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestNewWiresSubcommands(t *testing.T) {
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults()}, nil
	}
	opts := func() globals.Options { return globals.Options{} }
	cmd := New(rt, opts, io.Discard)

	if cmd.Use != "horde" {
		t.Fatalf("Use = %q, want horde", cmd.Use)
	}
	if cmd.Short != "Manage Unreal Horde build coordinator" {
		t.Errorf("Short = %q", cmd.Short)
	}
	if cmd.Long == "" {
		t.Error("Long should not be empty")
	}
}

func TestNewHasExpectedSubcommands(t *testing.T) {
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults()}, nil
	}
	opts := func() globals.Options { return globals.Options{} }
	cmd := New(rt, opts, io.Discard)

	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"create", "status", "submit", "destroy", "ami"} {
		if !names[want] {
			t.Errorf("missing subcommand %q", want)
		}
	}
}

func TestNewSubcommandCount(t *testing.T) {
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults()}, nil
	}
	opts := func() globals.Options { return globals.Options{} }
	cmd := New(rt, opts, io.Discard)

	if got := len(cmd.Commands()); got != 5 {
		t.Errorf("expected 5 subcommands, got %d", got)
	}
}

func TestRunWithoutSubcommandShowsHelp(t *testing.T) {
	rt := func() (globals.Runtime, error) {
		return globals.Runtime{Config: config.Defaults()}, nil
	}
	opts := func() globals.Options { return globals.Options{} }
	cmd := New(rt, opts, io.Discard)

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{})
	_ = cmd.Execute()

	got := out.String()
	if len(got) == 0 {
		t.Error("expected help output when no subcommand given")
	}
}
