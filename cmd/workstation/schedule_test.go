package workstation

import (
	"bytes"
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestWorkstationScheduleCobra(t *testing.T) {
	cfg := config.Defaults()
	cfg.Workstation.Schedule.Enabled = true
	src := func() (globals.Runtime, error) { return globals.Runtime{Config: cfg}, nil }
	var out bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&out)
	root.AddCommand(New(src, optionsSource, &out))
	got, err := testutil.RunCommandWithOut(t, root, &out, "workstation", "schedule")
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	testutil.AssertContains(t, got, "Schedule:")
}

func TestWorkstationScheduleCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) { return globals.Runtime{}, os.ErrNotExist }
	var out bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&out)
	root.AddCommand(New(src, optionsSource, &out))
	if _, err := testutil.RunCommandWithOut(t, root, &out, "workstation", "schedule"); err == nil {
		t.Fatal("expected runtime error")
	}
}
