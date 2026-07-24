package destroy

import (
	"bytes"
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
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
	var deletedSDK []string
	var deletedCloudControl []string

	tc := buildTeardownForTest(st, nil, func(ctx context.Context, typeName, identifier string) error {
		if typeName == "AWS::CodeBuild::Project" {
			deletedSDK = append(deletedSDK, identifier)
			return nil
		}
		return cloud.ErrNotHandled
	}, func(ctx context.Context, r *cloud.Resource) error {
		deletedCloudControl = append(deletedCloudControl, r.Identifier)
		return nil
	})

	if err := tc.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(deletedSDK) != 1 || deletedSDK[0] != "fabrica-ci" {
		t.Fatalf("project delete = %v, want [fabrica-ci]", deletedSDK)
	}
	if len(deletedCloudControl) != 1 || deletedCloudControl[0] != "fabrica-ci-codebuild" {
		t.Fatalf("role delete = %v, want [fabrica-ci-codebuild]", deletedCloudControl)
	}
	if st.GetModule("ci") != nil {
		t.Fatal("ci module should be removed from state after teardown")
	}
}

func TestRunNotProvisioned(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	var out bytes.Buffer

	tc := teardown.Command{
		Spec: teardown.Spec{
			ModuleName:     "ci",
			Verb:           "destroy",
			VersionLabel:   "Project",
			Title:          "CI",
			NotProvisioned: "CI is not provisioned. Nothing to destroy.",
		},
		Out:       &out,
		ReadState: func() (*fabricastate.State, error) { return st, nil },
	}

	if err := tc.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("not provisioned")) {
		t.Fatalf("expected not-provisioned message, got:\n%s", out.String())
	}
}

func TestRunProjectMissingIsNotError(t *testing.T) {
	st := seededCIState()
	var out bytes.Buffer

	tc := buildTeardownForTest(st, nil,
		func(ctx context.Context, typeName, identifier string) error {
			// Models CodeBuildRunner returning nil for missing project.
			return nil
		},
		func(ctx context.Context, r *cloud.Resource) error { return nil },
	)
	tc.Out = &out

	if err := tc.Run(context.Background()); err != nil {
		t.Fatalf("run should tolerate missing project: %v", err)
	}
}

func TestRunDryRunListsResources(t *testing.T) {
	st := seededCIState()
	var out bytes.Buffer

	tc := buildTeardownForTest(st, nil, nil, nil)
	tc.DryRun = true
	tc.Out = &out

	if err := tc.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !bytes.Contains(out.Bytes(), []byte("dry run")) || !bytes.Contains(out.Bytes(), []byte("fabrica-ci")) {
		t.Fatalf("dry-run should list resources, got:\n%s", out.String())
	}
}

func TestRunProjectDeleteErrorPropagates(t *testing.T) {
	st := seededCIState()
	var roleDeleted bool

	tc := buildTeardownForTest(st, nil,
		func(ctx context.Context, typeName, identifier string) error {
			if typeName == "AWS::CodeBuild::Project" {
				return errContext("codebuild boom")
			}
			return nil
		},
		func(ctx context.Context, r *cloud.Resource) error {
			roleDeleted = true
			return nil
		},
	)

	err := tc.Run(context.Background())
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("codebuild boom")) {
		t.Fatalf("expected project delete error to propagate, got: %v", err)
	}
	if roleDeleted {
		t.Fatal("role delete must not run after project error")
	}
}

func TestRunRoleDeleteErrorPropagates(t *testing.T) {
	st := seededCIState()

	tc := buildTeardownForTest(st, nil,
		func(ctx context.Context, typeName, identifier string) error {
			if typeName == "AWS::CodeBuild::Project" {
				return nil
			}
			return cloud.ErrNotHandled
		},
		func(ctx context.Context, r *cloud.Resource) error {
			return errContext("iam boom")
		},
	)

	err := tc.Run(context.Background())
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("iam boom")) {
		t.Fatalf("expected role delete error to propagate, got: %v", err)
	}
}

// errContext is a tiny error helper so these tests need no extra imports.
type errContext string

func (e errContext) Error() string { return string(e) }

func TestRunOrchestratedNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	rt := globals.Runtime{Config: &config.Config{}}
	if err := RunOrchestrated(context.Background(), rt, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunOrchestrated on empty state: %v", err)
	}
}

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

// buildTeardownForTest constructs a teardown.Command with CI-specific specs
// and injected test seams.
func buildTeardownForTest(st *fabricastate.State, rt *globals.Runtime, sdkDelete func(ctx context.Context, typeName, identifier string) error, deleteResource func(ctx context.Context, r *cloud.Resource) error) teardown.Command {
	runtime := globals.Runtime{}
	if rt != nil {
		runtime = *rt
	}
	tc := teardown.Command{
		Spec: teardown.Spec{
			ModuleName:     "ci",
			Verb:           "destroy",
			VersionLabel:   "Project",
			Title:          "CI",
			NotProvisioned: "CI is not provisioned. Nothing to destroy.",
			PlanHeader:     "CI — destroy plan",
			DryRunHeader:   "CI (destroy dry run)",
			Irreversible:   "IRREVERSIBLE: deletes the CodeBuild project and IAM role.",
			SuccessMessage: "CI infrastructure destroyed.",
			ResourceOrder:  ciResourceOrder,
		},
		Runtime:     runtime,
		SkipConfirm: true,
		Out:         &bytes.Buffer{},
		ReadState:   func() (*fabricastate.State, error) { return st, nil },
		WriteState:  func(*fabricastate.State) error { return nil },
	}
	if sdkDelete != nil {
		tc.SDKDeleteFunc = sdkDelete
	}
	if deleteResource != nil {
		tc.DeleteResource = deleteResource
	}
	return tc
}

type testCBProvider struct{}

func (p *testCBProvider) Name() string { return "test-cb" }
func (p *testCBProvider) Identity(ctx context.Context) (string, string, string, error) {
	return "123456789012", "arn:aws:iam::123456789012:user/test", "us-east-1", nil
}
func (p *testCBProvider) Resources() cloud.ResourceClient                      { return &testRC{} }
func (p *testCBProvider) DeleteProject(ctx context.Context, name string) error { return nil }

type testRC struct{}

func (r *testRC) Create(context.Context, *cloud.Resource) error { return nil }
func (r *testRC) Get(context.Context, *cloud.Resource) error    { return nil }
func (r *testRC) Update(context.Context, *cloud.Resource) error { return nil }
func (r *testRC) Delete(context.Context, *cloud.Resource) error { return nil }
func (r *testRC) List(context.Context, string) ([]cloud.Resource, error) {
	return nil, nil
}
