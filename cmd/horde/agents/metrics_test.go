package agents

import (
	"bytes"
	"os"
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

func TestAgentsMetricsJSONAndCustom(t *testing.T) {
	cfg := config.Defaults()
	cfg.Horde.Agents.Scaling.MetricName = "QueueDepth"
	cfg.Horde.Agents.Scaling.MetricNamespace = "Studio/Horde"
	src := func() (globals.Runtime, error) { return globals.Runtime{Config: cfg}, nil }
	var out bytes.Buffer
	root, _ := testutil.BuildTestSubcommand(&out)
	opts := func() globals.Options { return globals.Options{JSONOutput: true} }
	root.AddCommand(New(src, opts, &out))
	got, err := testutil.RunCommandWithOut(t, root, &out, "agents", "metrics", "--json")
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertContains(t, got, `"metricName": "QueueDepth"`)
}

func TestAgentsMetricsRuntimeError(t *testing.T) {
	src := func() (globals.Runtime, error) { return globals.Runtime{}, os.ErrNotExist }
	var out bytes.Buffer
	root, optionsSource := testutil.BuildTestSubcommand(&out)
	root.AddCommand(New(src, optionsSource, &out))
	if _, err := testutil.RunCommandWithOut(t, root, &out, "agents", "metrics"); err == nil {
		t.Fatal("expected runtime error")
	}
}
