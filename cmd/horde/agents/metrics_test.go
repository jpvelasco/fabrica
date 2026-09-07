package agents

import (
	"bytes"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestAgentsMetricsCobra(t *testing.T) {
	src := func() (globals.Runtime, error) { return globals.Runtime{Config: config.Defaults()}, nil }
	var out bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&out)
	root.AddCommand(New(src, optionsSource, &out))
	got, err := testutil.RunCommandWithOut(t, root, &out, "agents", "metrics")
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertContains(t, got, "ASGQueueDepth")
}
