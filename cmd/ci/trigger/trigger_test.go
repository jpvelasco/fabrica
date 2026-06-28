package trigger

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

type fakeRunner struct {
	startProject string
	startEnv     map[string]string
	startID      string
	startErr     error
	statuses     []cloud.BuildInfo
	statusIdx    int
}

func (f *fakeRunner) StartBuild(_ context.Context, project string, env map[string]string) (string, error) {
	f.startProject = project
	f.startEnv = env
	return f.startID, f.startErr
}

func (f *fakeRunner) BuildStatus(_ context.Context, _ string) (cloud.BuildInfo, error) {
	if f.statusIdx < len(f.statuses) {
		s := f.statuses[f.statusIdx]
		f.statusIdx++
		return s, nil
	}
	return cloud.BuildInfo{Status: "SUCCEEDED"}, nil
}

func (f *fakeRunner) BuildLog(_ context.Context, _ string) (string, error) { return "", nil }
func (f *fakeRunner) EnsureProject(_ context.Context, _ cloud.CodeBuildProjectSpec) (bool, error) {
	return true, nil
}
func (f *fakeRunner) DeleteProject(_ context.Context, _ string) error { return nil }

func writeTempBuildGraph(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "BuildGraph.xml")
	xml := `<?xml version="1.0"?><BuildGraph xmlns="http://www.epicgames.com/BuildGraph">
		<Agent Name="BuildAgent" Type="Win64"><Node Name="Compile"/></Agent>
	</BuildGraph>`
	if err := os.WriteFile(path, []byte(xml), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func provisionedState() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "fabrica-ci", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci"},
	})
	st.UpsertModule("horde", "", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-horde123"},
	})
	return st
}

func newCmd(out *bytes.Buffer, runner cloud.CodeBuildRunner, st *fabricastate.State) command {
	return command{
		runtime:        globals.Runtime{Config: config.Defaults()},
		buildGraphPath: "",
		out:            out,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		getResource: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = []byte(`{"PrivateIpAddress":"10.0.1.42"}`)
			return nil
		},
		runner: runner,
		sleep:  func(time.Duration) {},
		now:    time.Now,
	}
}

func TestTriggerStartsBuildWithHordeEnv(t *testing.T) {
	var out bytes.Buffer
	runner := &fakeRunner{startID: "build-1"}
	c := newCmd(&out, runner, provisionedState())
	c.buildGraphPath = writeTempBuildGraph(t)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if runner.startProject != "fabrica-ci" {
		t.Errorf("project = %q", runner.startProject)
	}
	if runner.startEnv["HORDE_URL"] != "http://10.0.1.42:5000" {
		t.Errorf("HORDE_URL = %q", runner.startEnv["HORDE_URL"])
	}
	if runner.startEnv["TARGET"] != "Compile" {
		t.Errorf("TARGET = %q", runner.startEnv["TARGET"])
	}
	if !strings.Contains(out.String(), "Build started: build-1") {
		t.Errorf("missing start message:\n%s", out.String())
	}
}

func TestTriggerErrorsWhenCINotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1") // no ci module
	c := newCmd(&out, &fakeRunner{startID: "x"}, st)
	c.buildGraphPath = writeTempBuildGraph(t)
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when CI not provisioned")
	}
}

func TestTriggerErrorsWhenHordeNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "fabrica-ci", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci"},
	})
	c := newCmd(&out, &fakeRunner{startID: "x"}, st)
	c.buildGraphPath = writeTempBuildGraph(t)
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when Horde not provisioned")
	}
}

func TestTriggerBadBuildGraphFailsFast(t *testing.T) {
	var out bytes.Buffer
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.xml")
	_ = os.WriteFile(bad, []byte("not xml <<<"), 0644)
	c := newCmd(&out, &fakeRunner{startID: "x"}, provisionedState())
	c.buildGraphPath = bad
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestTriggerStartBuildErrorPropagates(t *testing.T) {
	var out bytes.Buffer
	c := newCmd(&out, &fakeRunner{startErr: errors.New("boom")}, provisionedState())
	c.buildGraphPath = writeTempBuildGraph(t)
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected start error")
	}
}

func TestTriggerWaitPollsToTerminal(t *testing.T) {
	var out bytes.Buffer
	runner := &fakeRunner{
		startID: "build-1",
		statuses: []cloud.BuildInfo{
			{Status: "IN_PROGRESS", Phase: "BUILD"},
			{Status: "SUCCEEDED", Phase: "COMPLETED"},
		},
	}
	c := newCmd(&out, runner, provisionedState())
	c.buildGraphPath = writeTempBuildGraph(t)
	c.wait = true
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "SUCCEEDED") {
		t.Errorf("expected terminal status in output:\n%s", out.String())
	}
}

func TestTriggerWaitFailedBuildReturnsError(t *testing.T) {
	var out bytes.Buffer
	runner := &fakeRunner{
		startID:  "build-1",
		statuses: []cloud.BuildInfo{{Status: "FAILED", Phase: "BUILD"}},
	}
	c := newCmd(&out, runner, provisionedState())
	c.buildGraphPath = writeTempBuildGraph(t)
	c.wait = true
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error for failed build")
	}
}
