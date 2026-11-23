package trigger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/horde"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

type fakeRunner struct {
	startProject   string
	startEnv       map[string]string
	startBuildspec string
	startID        string
	startErr       error
	statuses       []cloud.BuildInfo
	statusIdx      int
}

func (f *fakeRunner) StartBuild(_ context.Context, project string, env map[string]string, buildspec string) (string, error) {
	f.startProject = project
	f.startEnv = env
	f.startBuildspec = buildspec
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

// testStream and testTemplate are the horde.streamId and horde.templateId the
// trigger tests configure; jobConfig is the only place they are set.
const (
	testStream   = "//UE5/Main"
	testTemplate = "build-template"
)

// jobConfig is the default config with the stream and template a trigger needs.
func jobConfig() *config.Config {
	cfg := config.Defaults()
	cfg.Horde.StreamID = testStream
	cfg.Horde.TemplateID = testTemplate
	return cfg
}

// decodeJobRequest returns the HORDE_JOB_REQUEST env value as a generic map, so
// assertions see exactly the keys the buildspec receives.
func decodeJobRequest(t *testing.T, env map[string]string) map[string]any {
	t.Helper()
	raw, ok := env["HORDE_JOB_REQUEST"]
	if !ok {
		t.Fatalf("HORDE_JOB_REQUEST not in env: %v", env)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("HORDE_JOB_REQUEST is not JSON: %v (%q)", err, raw)
	}
	return got
}

func newCmd(out *bytes.Buffer, runner cloud.CodeBuildRunner, st *fabricastate.State) command {
	return command{
		runtime:        globals.Runtime{Config: jobConfig()},
		buildGraphPath: "",
		out:            out,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		getResource: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = []byte(`{"PrivateIpAddress":"10.0.1.42"}`)
			return nil
		},
		runner:  runner,
		waitCtx: func(context.Context, time.Duration) error { return nil },
		now:     time.Now,
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
	wantReq := map[string]any{"streamId": testStream, "templateId": testTemplate, "name": "BuildAgent", "targets": []any{"Compile"}}
	if got := decodeJobRequest(t, runner.startEnv); !reflect.DeepEqual(got, wantReq) {
		t.Errorf("HORDE_JOB_REQUEST = %v, want %v", got, wantReq)
	}
	if !strings.Contains(out.String(), "Build started: build-1") {
		t.Errorf("missing start message:\n%s", out.String())
	}
}

func TestTriggerConfigChangeSentAsCommitID(t *testing.T) {
	var out bytes.Buffer
	runner := &fakeRunner{startID: "build-1"}
	c := newCmd(&out, runner, provisionedState())
	c.buildGraphPath = writeTempBuildGraph(t)
	c.runtime.Config.Horde.Change = 7

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := decodeJobRequest(t, runner.startEnv)["commitId"]; got != "7" {
		t.Errorf("commitId = %v, want \"7\" from horde.change", got)
	}
}

// TestTriggerFlagsOverrideConfig checks that per-invocation overrides win over
// horde.* config, and that a request never carries an arguments field.
func TestTriggerFlagsOverrideConfig(t *testing.T) {
	var out bytes.Buffer
	runner := &fakeRunner{startID: "build-1"}
	c := newCmd(&out, runner, provisionedState())
	c.buildGraphPath = writeTempBuildGraph(t)
	c.runtime.Config.Horde.Change = 7
	c.overrides = horde.JobOverrides{StreamID: "//Flag/Stream", TemplateID: "flag-template", Change: 4242, Name: "Nightly", Target: "Compile"}

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	wantReq := map[string]any{"streamId": "//Flag/Stream", "templateId": "flag-template", "name": "Nightly", "commitId": "4242", "targets": []any{"Compile"}}
	if got := decodeJobRequest(t, runner.startEnv); !reflect.DeepEqual(got, wantReq) {
		t.Errorf("HORDE_JOB_REQUEST = %v, want %v", got, wantReq)
	}
}

func TestTriggerUnknownTargetWarnsAndIsSent(t *testing.T) {
	var out bytes.Buffer
	runner := &fakeRunner{startID: "build-1"}
	c := newCmd(&out, runner, provisionedState())
	c.buildGraphPath = writeTempBuildGraph(t)
	c.overrides.Target = "Cook"

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "Warning: target \"Cook\"") {
		t.Errorf("expected unknown-target warning:\n%s", out.String())
	}
	if got := decodeJobRequest(t, runner.startEnv)["targets"]; !reflect.DeepEqual(got, []any{"Cook"}) {
		t.Errorf("targets = %v, want [Cook]", got)
	}
}

// TestTriggerInvalidJobRequestFailsBeforeState checks that a missing stream or
// template, or a bad change, fails before any state read or CodeBuild call,
// including under --dry-run.
func TestTriggerInvalidJobRequestFailsBeforeState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*command)
		wantErr string
	}{
		{"missing stream", func(c *command) { c.runtime.Config.Horde.StreamID = "" }, "need a stream and template"},
		{"missing template", func(c *command) { c.runtime.Config.Horde.TemplateID = "" }, "need a stream and template"},
		{"no config loaded", func(c *command) { c.runtime.Config = nil }, "need a stream and template"},
		{"bad change", func(c *command) { c.overrides.Change = -3 }, "not a positive Perforce changelist"},
	} {
		for _, dryRun := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dry_run=%v", tc.name, dryRun), func(t *testing.T) {
				var out bytes.Buffer
				runner := &fakeRunner{startID: "build-1"}
				c := newCmd(&out, runner, provisionedState())
				c.buildGraphPath = writeTempBuildGraph(t)
				c.dryRun = dryRun
				c.readState = func() (*fabricastate.State, error) {
					t.Fatal("state read before the job request was validated")
					return nil, nil
				}
				tc.mutate(&c)

				err := c.run(context.Background())
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("run() error = %v, want %q", err, tc.wantErr)
				}
				if runner.startProject != "" {
					t.Errorf("CodeBuild started for an invalid request: %q", runner.startProject)
				}
			})
		}
	}
}

func TestTriggerSendsCurrentBuildspec(t *testing.T) {
	var out bytes.Buffer
	runner := &fakeRunner{startID: "build-1"}
	c := newCmd(&out, runner, provisionedState())
	c.buildGraphPath = writeTempBuildGraph(t)

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{"version: 0.2", `--data-binary "$HORDE_JOB_REQUEST"`, `test -n "$HORDE_JOB_REQUEST"`} {
		if !strings.Contains(runner.startBuildspec, want) {
			t.Errorf("buildspec override missing %q", want)
		}
	}
}

func TestTriggerDryRunNoStart(t *testing.T) {
	var out bytes.Buffer
	runner := &fakeRunner{startID: "build-1"}
	c := newCmd(&out, runner, provisionedState())
	c.buildGraphPath = writeTempBuildGraph(t)
	c.dryRun = true

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if runner.startProject != "" {
		t.Errorf("dry run must not start a build, got project %q", runner.startProject)
	}
	if !strings.Contains(out.String(), "dry run") {
		t.Errorf("expected dry-run preview header:\n%s", out.String())
	}
	for _, want := range []string{"fabrica-ci", "Compile", "http://10.0.1.42:5000", testStream, testTemplate} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("preview missing %q:\n%s", want, out.String())
		}
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

func (f *alwaysInProgressRunner) StartBuild(_ context.Context, project string, env map[string]string, _ string) (string, error) {
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
		runtime:        globals.Runtime{Config: jobConfig()},
		buildGraphPath: writeTempBuildGraph(t),
		out:            &out,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		waitCtx:        func(context.Context, time.Duration) error { return nil },
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
		runtime:        globals.Runtime{Config: jobConfig()},
		buildGraphPath: writeTempBuildGraph(t),
		out:            &out,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		getResource:    nil,
		runner:         &fakeRunner{startID: "x"},
		waitCtx:        func(context.Context, time.Duration) error { return nil },
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
		runtime:        globals.Runtime{Config: jobConfig()},
		buildGraphPath: writeTempBuildGraph(t),
		out:            &out,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		getResource: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = []byte(`{}`)
			return nil
		},
		runner:  &fakeRunner{startID: "x"},
		waitCtx: func(context.Context, time.Duration) error { return nil },
		now:     time.Now,
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when no private IP")
	}
}

func TestTriggerResolveHordeGetResourceError(t *testing.T) {
	var out bytes.Buffer
	st := provisionedState()
	c := command{
		runtime:        globals.Runtime{Config: jobConfig()},
		buildGraphPath: writeTempBuildGraph(t),
		out:            &out,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		getResource:    func(_ context.Context, _ *cloud.Resource) error { return errors.New("provider error") },
		runner:         &fakeRunner{startID: "x"},
		waitCtx:        func(context.Context, time.Duration) error { return nil },
		now:            time.Now,
	}
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error when Get fails")
	}
}

func TestTriggerCustomHordePort(t *testing.T) {
	var out bytes.Buffer
	cfg := jobConfig()
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
		runner:  runner,
		waitCtx: func(context.Context, time.Duration) error { return nil },
		now:     time.Now,
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
		runtime:        globals.Runtime{Config: jobConfig()},
		buildGraphPath: writeTempBuildGraph(t),
		out:            &out,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		getResource: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = []byte(`{"PrivateIpAddress":"10.0.1.42"}`)
			return nil
		},
		runner:  &failingStatusRunner{},
		waitCtx: func(context.Context, time.Duration) error { return nil },
		now:     time.Now,
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
		runtime:        globals.Runtime{Config: jobConfig()},
		buildGraphPath: writeTempBuildGraph(t),
		out:            &out,
		readState:      func() (*fabricastate.State, error) { return nil, errors.New("state read error") },
		runner:         &fakeRunner{startID: "x"},
		waitCtx:        func(context.Context, time.Duration) error { return nil },
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

// TestTriggerWaitCancelled verifies cancellation during the polling interval
// ends the wait immediately with a notice instead of hanging.
func TestTriggerWaitCancelled(t *testing.T) {
	var out bytes.Buffer
	c := newCmd(&out, &alwaysInProgressRunner{startID: "build-1"}, provisionedState())
	c.buildGraphPath = writeTempBuildGraph(t)
	c.wait = true
	c.waitCtx = func(context.Context, time.Duration) error { return context.Canceled }

	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "waiting cancelled") {
		t.Errorf("expected cancellation notice:\n%s", out.String())
	}
}
