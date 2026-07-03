package destroy

import (
	"bytes"
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func seededCIState() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "fabrica-ci", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-ci-codebuild"},
		{TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci"},
	})
	return st
}

func TestRunDeletesProjectThenRole(t *testing.T) {
	st := seededCIState()
	var deletedProject string
	var deletedResources []string
	c := command{
		runtime:     globals.Runtime{},
		out:         &bytes.Buffer{},
		skipConfirm: true,
		readState:   func() (*fabricastate.State, error) { return st, nil },
		writeState:  func(*fabricastate.State) error { return nil },
		deleteProject: func(_ context.Context, name string) error {
			deletedProject = name
			return nil
		},
		deleteResource: func(_ context.Context, r *cloud.Resource) error {
			deletedResources = append(deletedResources, r.Identifier)
			return nil
		},
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if deletedProject != "fabrica-ci" {
		t.Fatalf("project delete = %q, want fabrica-ci", deletedProject)
	}
	if len(deletedResources) != 1 || deletedResources[0] != "fabrica-ci-codebuild" {
		t.Fatalf("role delete = %v, want [fabrica-ci-codebuild]", deletedResources)
	}
	if st.GetModule("ci") != nil {
		t.Fatal("ci module should be removed from state after teardown")
	}
}

func TestRunNotProvisioned(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	var out bytes.Buffer
	c := command{
		runtime:        globals.Runtime{},
		out:            &out,
		skipConfirm:    true,
		readState:      func() (*fabricastate.State, error) { return st, nil },
		writeState:     func(*fabricastate.State) error { return nil },
		deleteProject:  func(context.Context, string) error { t.Fatal("no delete expected"); return nil },
		deleteResource: func(context.Context, *cloud.Resource) error { t.Fatal("no delete expected"); return nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("not provisioned")) {
		t.Fatalf("expected not-provisioned message, got:\n%s", out.String())
	}
}

func TestRunProjectMissingIsNotError(t *testing.T) {
	st := seededCIState()
	c := command{
		runtime:     globals.Runtime{},
		out:         &bytes.Buffer{},
		skipConfirm: true,
		readState:   func() (*fabricastate.State, error) { return st, nil },
		writeState:  func(*fabricastate.State) error { return nil },
		// DeleteProject swallows missing-project per the CodeBuildRunner contract,
		// so a nil return here models that; run() must not error.
		deleteProject:  func(context.Context, string) error { return nil },
		deleteResource: func(context.Context, *cloud.Resource) error { return nil },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run should tolerate missing project: %v", err)
	}
}
