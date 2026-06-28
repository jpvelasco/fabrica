package status

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

type fakeRunner struct {
	info cloud.BuildInfo
	err  error
}

func (f fakeRunner) StartBuild(context.Context, string, map[string]string) (string, error) {
	return "", nil
}
func (f fakeRunner) BuildStatus(context.Context, string) (cloud.BuildInfo, error) {
	return f.info, f.err
}
func (f fakeRunner) BuildLog(context.Context, string) (string, error) { return "", nil }

func provisionedState() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "fabrica-ci", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-ci-codebuild"},
		{TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci"},
	})
	return st
}

func TestStatusNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &out,
		readState: func() (*fabricastate.State, error) { return fabricastate.NewState("a", "r"), nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "not provisioned") {
		t.Errorf("expected not-provisioned message:\n%s", out.String())
	}
}

func TestStatusShowsInfra(t *testing.T) {
	var out bytes.Buffer
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &out,
		readState: func() (*fabricastate.State, error) { return provisionedState(), nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := out.String()
	if !strings.Contains(s, "fabrica-ci") || !strings.Contains(s, "fabrica-ci-codebuild") {
		t.Errorf("expected project + role:\n%s", s)
	}
}

func TestStatusWithBuildID(t *testing.T) {
	var out bytes.Buffer
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		out:       &out,
		buildID:   "build-1",
		readState: func() (*fabricastate.State, error) { return provisionedState(), nil },
		runner:    fakeRunner{info: cloud.BuildInfo{ID: "build-1", Status: "SUCCEEDED", Phase: "COMPLETED"}},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "SUCCEEDED") {
		t.Errorf("expected build status:\n%s", out.String())
	}
}

func TestStatusJSON(t *testing.T) {
	var out bytes.Buffer
	c := command{
		runtime:   globals.Runtime{Config: config.Defaults()},
		jsonOut:   true,
		out:       &out,
		readState: func() (*fabricastate.State, error) { return provisionedState(), nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	var o StatusOutput
	if err := json.Unmarshal(out.Bytes(), &o); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out.String())
	}
	if !o.Provisioned || o.Project != "fabrica-ci" {
		t.Errorf("unexpected JSON: %+v", o)
	}
}
