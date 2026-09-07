package agents

import (
	"bytes"
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestAgentsScheduleCobra(t *testing.T) {
	cfg := config.Defaults()
	cfg.Horde.Agents.Spot = true
	src := func() (globals.Runtime, error) { return globals.Runtime{Config: cfg}, nil }
	var out bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&out)
	root.AddCommand(New(src, optionsSource, &out))
	got, err := testutil.RunCommandWithOut(t, root, &out, "agents", "schedule")
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	testutil.AssertContains(t, got, "Spot:")
}

func TestAgentsScheduleCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) { return globals.Runtime{}, os.ErrNotExist }
	var out bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&out)
	root.AddCommand(New(src, optionsSource, &out))
	if _, err := testutil.RunCommandWithOut(t, root, &out, "agents", "schedule"); err == nil {
		t.Fatal("expected runtime error")
	}
}
