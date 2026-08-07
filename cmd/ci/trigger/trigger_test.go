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
func (f *fakeRunner) DeleteProject(_ context.Context, _ string) error         { return nil }
func (f *fakeRunner) ProjectExists(_ context.Context, _ string) (bool, error) { return false, nil }

func writeTempBuildGraph(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "BuildGraph.xml")
	xml := `<?xml version="1.0"?><BuildGraph xmlns="http://www.epicgames.com/BuildGraph">
		<Agent Name="BuildAgent" Type="Win64"><Node Name="Compile"/></Agent>
	</BuildGraph>`
	if err := os.WriteFile(path, []byte(xml), 0600); err != nil {
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
	_ = os.WriteFile(bad, []byte("not xml <<<"), 0600)
	c := newCmd(&out, &fakeRunner{startID: "x"}, provisionedState())
	c.buildGraphPath = bad
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestTriggerEmptyTargetFailsFast(t *testing.T) {
	var out bytes.Buffer
	dir := t.TempDir()
	emptyTarget := filepath.Join(dir, "empty-target.xml")
	_ = os.WriteFile(emptyTarget, []byte(`<?xml version="1.0"?>
<BuildGraph xmlns="http://www.epicgames.com/BuildGraph">
    <Agent Name="EmptyAgent" Type="Win64"></Agent>
</BuildGraph>`), 0600)
	c := newCmd(&out, &fakeRunner{startID: "x"}, provisionedState())
	c.buildGraphPath = emptyTarget
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error for empty target")
	}
	if !strings.Contains(err.Error(), "no build target") {
		t.Errorf("error %q should mention 'no build target'", err.Error())
	}
	if !strings.Contains(err.Error(), "BuildGraph.sample.xml") {
		t.Errorf("error %q should reference sample file", err.Error())
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

func TestTriggerWaitTimeout(t *testing.T) {
	var out bytes.Buffer
	callCount := 0
	c := newCmd(&out, &alwaysInProgressRunner{startID: "build-1"}, provisionedState())
	c.buildGraphPath = writeTempBuildGraph(t)
	c.wait = true
	c.now = func() time.Time {
		callCount++
		if callCount > 2 {
			return time.Now().Add(waitDeadline + time.Second)
		}
		return time.Now()
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "Timed out") {
		t.Errorf("expected timeout message in output:\n%s", out.String())
	}
}

type alwaysInProgressRunner struct{ startID string }

func (f *alwaysInProgressRunner) StartBuild(_ context.Context, project string, env map[string]string) (string, error) {
	return f.startID, nil
}
func (f *alwaysInProgressRunner) BuildStatus(_ context.Context, _ string) (cloud.BuildInfo, error) {
	return cloud.BuildInfo{Status: "IN_PROGRESS", Phase: "BUILD"}, nil
}
func (f *alwaysInProgressRunner) BuildLog(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (f *alwaysInProgressRunner) EnsureProject(_ context.Context, _ cloud.CodeBuildProjectSpec) (bool, error) {
	return true, nil
}
func (f *alwaysInProgressRunner) DeleteProject(_ context.Context, _ string) error { return nil }
func (f *alwaysInProgressRunner) ProjectExists(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func TestTriggerNoRunnerErrors(t *testing.T) {
	var out bytes.Buffer
	st := provisionedState()
	c := command{
		runtime:        globals.Runtime{Config: config.Defaults()},
		buildGraphPath: writeTempBuildGraph(t),
		out:            &out,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		sleep:          func(time.Duration) {},
		now:            time.Now,
		runner:         nil,
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when no runner configured")
	}
}

func TestTriggerResolveProjectMissingIdentifier(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "fabrica-ci", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::CodeBuild::Project", Identifier: ""},
	})
	st.UpsertModule("horde", "", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-horde123"},
	})
	c := newCmd(&out, &fakeRunner{startID: "x"}, st)
	c.buildGraphPath = writeTempBuildGraph(t)
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when project identifier is empty")
	}
}

func TestTriggerResolveHordeNoGetInstance(t *testing.T) {
	var out bytes.Buffer
	st := provisionedState()
	c := command{
		runtime:        globals.Runtime{Config: config.Defaults()},
		buildGraphPath: writeTempBuildGraph(t),
		out:            &out,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		getResource:    nil,
		runner:         &fakeRunner{startID: "x"},
		sleep:          func(time.Duration) {},
		now:            time.Now,
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when getResource is nil")
	}
}

func TestTriggerResolveHordeNoPrivateIP(t *testing.T) {
	var out bytes.Buffer
	st := provisionedState()
	c := command{
		runtime:        globals.Runtime{Config: config.Defaults()},
		buildGraphPath: writeTempBuildGraph(t),
		out:            &out,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		getResource: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = []byte(`{}`)
			return nil
		},
		runner: &fakeRunner{startID: "x"},
		sleep:  func(time.Duration) {},
		now:    time.Now,
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when no private IP")
	}
}

func TestTriggerResolveHordeGetResourceError(t *testing.T) {
	var out bytes.Buffer
	st := provisionedState()
	c := command{
		runtime:        globals.Runtime{Config: config.Defaults()},
		buildGraphPath: writeTempBuildGraph(t),
		out:            &out,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		getResource:    func(_ context.Context, _ *cloud.Resource) error { return errors.New("provider error") },
		runner:         &fakeRunner{startID: "x"},
		sleep:          func(time.Duration) {},
		now:            time.Now,
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when Get fails")
	}
}

func TestTriggerCustomHordePort(t *testing.T) {
	var out bytes.Buffer
	cfg := config.Defaults()
	cfg.Horde.Port = 5555
	st := provisionedState()
	runner := &fakeRunner{startID: "x"}
	c := command{
		runtime:        globals.Runtime{Config: cfg},
		buildGraphPath: writeTempBuildGraph(t),
		out:            &out,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		getResource: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = []byte(`{"PrivateIpAddress":"10.0.1.42"}`)
			return nil
		},
		runner: runner,
		sleep:  func(time.Duration) {},
		now:    time.Now,
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if runner.startEnv["HORDE_URL"] != "http://10.0.1.42:5555" {
		t.Errorf("HORDE_URL = %q, want http://10.0.1.42:5555", runner.startEnv["HORDE_URL"])
	}
}

func TestTriggerWaitBuildStatusError(t *testing.T) {
	var out bytes.Buffer
	st := provisionedState()
	c := command{
		runtime:        globals.Runtime{Config: config.Defaults()},
		buildGraphPath: writeTempBuildGraph(t),
		out:            &out,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		getResource: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = []byte(`{"PrivateIpAddress":"10.0.1.42"}`)
			return nil
		},
		runner: &failingStatusRunner{},
		sleep:  func(time.Duration) {},
		now:    time.Now,
	}
	c.wait = true
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when BuildStatus fails")
	}
}

type failingStatusRunner struct {
	fakeRunner
}

func (f *failingStatusRunner) BuildStatus(_ context.Context, _ string) (cloud.BuildInfo, error) {
	return cloud.BuildInfo{}, errors.New("status unavailable")
}

func TestPrivateIPEmptyInput(t *testing.T) {
	if got := privateIP(nil); got != "" {
		t.Errorf("expected empty string for nil input, got %q", got)
	}
	if got := privateIP([]byte{}); got != "" {
		t.Errorf("expected empty string for empty input, got %q", got)
	}
}

func TestPrivateIPInvalidJSON(t *testing.T) {
	if got := privateIP([]byte("not json")); got != "" {
		t.Errorf("expected empty string for invalid JSON, got %q", got)
	}
}

func TestPrivateIPValid(t *testing.T) {
	got := privateIP([]byte(`{"PrivateIpAddress":"10.0.5.99"}`))
	if got != "10.0.5.99" {
		t.Errorf("privateIP = %q, want 10.0.5.99", got)
	}
}

func TestIsTerminal(t *testing.T) {
	for _, s := range []string{"SUCCEEDED", "FAILED", "FAULT", "STOPPED", "TIMED_OUT"} {
		if !isTerminal(s) {
			t.Errorf("isTerminal(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"IN_PROGRESS", "QUEUED", "SUBMITTED", ""} {
		if isTerminal(s) {
			t.Errorf("isTerminal(%q) = true, want false", s)
		}
	}
}

func TestTriggerReadStateError(t *testing.T) {
	var out bytes.Buffer
	c := command{
		runtime:        globals.Runtime{Config: config.Defaults()},
		buildGraphPath: writeTempBuildGraph(t),
		out:            &out,
		readState:      func() (*fabricastate.State, error) { return nil, errors.New("state read error") },
		runner:         &fakeRunner{startID: "x"},
		sleep:          func(time.Duration) {},
		now:            time.Now,
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when readState fails")
	}
}

func TestTriggerHordeInstanceMissingFromState(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "fabrica-ci", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci"},
	})
	st.UpsertModule("horde", "", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-123"},
	})
	c := newCmd(&out, &fakeRunner{startID: "x"}, st)
	c.buildGraphPath = writeTempBuildGraph(t)
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when horde instance not in state")
	}
}
