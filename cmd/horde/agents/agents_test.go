package agents

import (
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
)

func TestNew_CommandStructure(t *testing.T) {
	runtimeSource := func() (globals.Runtime, error) { return globals.Runtime{}, nil }
	optionsSource := func() globals.Options { return globals.Options{} }

	cmd := New(runtimeSource, optionsSource, io.Discard)

	if cmd.Use != "agents" {
		t.Errorf("Use = %q, want agents", cmd.Use)
	}
	if cmd.Short != "Manage Horde build agent pool" {
		t.Errorf("Short = %q", cmd.Short)
	}
	if cmd.Long == "" {
		t.Error("Long should not be empty")
	}

	// Check subcommands are wired.
	if len(cmd.Commands()) != 3 {
		t.Errorf("want 3 subcommands, got %d", len(cmd.Commands()))
	}

	// Verify subcommand names.
	names := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		names[sub.Use] = true
	}
	for _, want := range []string{"create", "status", "destroy"} {
		if !names[want] {
			t.Errorf("missing subcommand: %s", want)
		}
	}
}
