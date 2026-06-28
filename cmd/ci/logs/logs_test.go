package logs

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
)

type fakeRunner struct {
	log string
	err error
}

func (f fakeRunner) StartBuild(context.Context, string, map[string]string) (string, error) {
	return "", nil
}
func (f fakeRunner) BuildStatus(context.Context, string) (cloud.BuildInfo, error) {
	return cloud.BuildInfo{}, nil
}
func (f fakeRunner) BuildLog(context.Context, string) (string, error) { return f.log, f.err }

func TestLogsPrintsOutput(t *testing.T) {
	var out bytes.Buffer
	c := command{runtime: globals.Runtime{}, buildID: "build-1", out: &out, runner: fakeRunner{log: "build log line\n"}}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "build log line") {
		t.Errorf("expected log output:\n%s", out.String())
	}
}

func TestLogsEmpty(t *testing.T) {
	var out bytes.Buffer
	c := command{runtime: globals.Runtime{}, buildID: "build-1", out: &out, runner: fakeRunner{log: ""}}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "No log output yet") {
		t.Errorf("expected empty-log message:\n%s", out.String())
	}
}

func TestLogsErrorPropagates(t *testing.T) {
	var out bytes.Buffer
	c := command{runtime: globals.Runtime{}, buildID: "build-1", out: &out, runner: fakeRunner{err: errors.New("boom")}}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestLogsNoRunner(t *testing.T) {
	var out bytes.Buffer
	c := command{runtime: globals.Runtime{}, buildID: "build-1", out: &out}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when no runner")
	}
}
