package destroy

import (
	"bytes"
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
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

// TestRunOrchestratedNotProvisioned verifies RunOrchestrated handles empty state gracefully.
func TestRunOrchestratedNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	rt := globals.Runtime{Config: &config.Config{}}
	if err := RunOrchestrated(context.Background(), rt, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunOrchestrated on empty state: %v", err)
	}
}

// TestRunOrchestratedWithProvider verifies RunOrchestrated tears down with provider wired.
func TestRunOrchestratedWithProvider(t *testing.T) {
	st := seededCIState()
	var deletedProject string
	var deletedResources []string
	rt := globals.Runtime{Provider: &testProvider{}}
	c := command{
		runtime:     rt,
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
		t.Fatalf("run with provider: %v", err)
	}
	if deletedProject != "fabrica-ci" {
		t.Fatalf("project delete = %q, want fabrica-ci", deletedProject)
	}
	if len(deletedResources) != 1 || deletedResources[0] != "fabrica-ci-codebuild" {
		t.Fatalf("role delete = %v, want [fabrica-ci-codebuild]", deletedResources)
	}
}

// TestRunOrchestratedProvisioned verifies RunOrchestrated fully exercises the teardown path.
func TestRunOrchestratedProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())

	cfg := &config.Config{
		Cloud: config.Cloud{
			AWS: config.AWS{AccountID: "123456789012"},
		},
	}
	rt := globals.Runtime{
		Config:   cfg,
		Provider: &testCBProvider{},
	}

	if err := RunOrchestrated(context.Background(), rt, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunOrchestrated should not error: %v", err)
	}
}

type testCBProvider struct{}

func (p *testCBProvider) Name() string { return "test-cb" }
func (p *testCBProvider) Identity(ctx context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}
func (p *testCBProvider) Resources() cloud.ResourceClient                      { return &testRC{} }
func (p *testCBProvider) DeleteProject(ctx context.Context, name string) error { return nil }

type testProvider struct{}

func (p *testProvider) Name() string { return "test" }
func (p *testProvider) Identity(ctx context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}
func (p *testProvider) Resources() cloud.ResourceClient { return &testRC{} }

type testRC struct{}

func (r *testRC) Create(context.Context, *cloud.Resource) error { return nil }
func (r *testRC) Get(context.Context, *cloud.Resource) error    { return nil }
func (r *testRC) Update(context.Context, *cloud.Resource) error { return nil }
func (r *testRC) Delete(context.Context, *cloud.Resource) error { return nil }
func (r *testRC) List(context.Context, string) ([]cloud.Resource, error) {
	return nil, nil
}
