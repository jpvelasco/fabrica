package ci

import (
	"bytes"
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestPipelineCobra(t *testing.T) {
	src := func() (globals.Runtime, error) { return globals.Runtime{Config: config.Defaults()}, nil }
	var out bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&out)
	root.AddCommand(New(src, optionsSource, &out))
	got, err := testutil.RunCommandWithOut(t, root, &out, "ci", "pipeline")
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertContains(t, got, "Pipeline:")
}

func TestPipelineCobraJSON(t *testing.T) {
	cfg := config.Defaults()
	cfg.CI.Pipeline = true
	cfg.CI.ProjectName = "studio"
	src := func() (globals.Runtime, error) { return globals.Runtime{Config: cfg}, nil }
	var out bytes.Buffer
	root, _ := testutil.BuildTestSubcommand(&out)
	opts := func() globals.Options { return globals.Options{JSONOutput: true} }
	root.AddCommand(New(src, opts, &out))
	got, err := testutil.RunCommandWithOut(t, root, &out, "ci", "pipeline", "--json")
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertContains(t, got, `"enabled": true`)
}

func TestPipelineCobraRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) { return globals.Runtime{}, os.ErrNotExist }
	var out bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&out)
	root.AddCommand(New(src, optionsSource, &out))
	if _, err := testutil.RunCommandWithOut(t, root, &out, "ci", "pipeline"); err == nil {
		t.Fatal("expected runtime error")
	}
}
