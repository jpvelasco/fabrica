package destroy

import (
	"bytes"
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func seededCIState() *fabricastate.State {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("ci", "fabrica-ci", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-ci-codebuild"},
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "fabrica-ci-sg"},
		{TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci"},
	})
	return st
}

func TestRunDeletesProjectThenSGThenRole(t *testing.T) {
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
	// Security group and IAM role deleted via Cloud Control, in order.
	if len(deletedCloudControl) != 2 {
		t.Fatalf("cloud control deletes = %v, want 2 (SG + role)", deletedCloudControl)
	}
	if deletedCloudControl[0] != "fabrica-ci-sg" {
		t.Fatalf("SG delete = %v, want fabrica-ci-sg", deletedCloudControl[0])
	}
	if deletedCloudControl[1] != "fabrica-ci-codebuild" {
		t.Fatalf("role delete = %v, want fabrica-ci-codebuild", deletedCloudControl[1])
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
	var anythingDeleted bool

	tc := buildTeardownForTest(st, nil,
		func(ctx context.Context, typeName, identifier string) error {
			if typeName == "AWS::CodeBuild::Project" {
				return errContext("codebuild boom")
			}
			return nil
		},
		func(ctx context.Context, r *cloud.Resource) error {
			anythingDeleted = true
			return nil
		},
	)

	err := tc.Run(context.Background())
	if err == nil || !bytes.Contains([]byte(err.Error()), []byte("codebuild boom")) {
		t.Fatalf("expected project delete error to propagate, got: %v", err)
	}
	if anythingDeleted {
		t.Fatal("no Cloud Control delete must run after project error")
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
		Provider: &testutil.CodeBuildProvider{},
	}

	if err := RunOrchestrated(context.Background(), rt, &bytes.Buffer{}); err != nil {
		t.Fatalf("RunOrchestrated should not error: %v", err)
	}
}

func TestCiResourceOrderIncludesSecurityGroup(t *testing.T) {
	st := seededCIState()
	m := st.GetModule("ci")
	if m == nil {
		t.Fatal("ci module not found")
	}

	resources := ciResourceOrder(m)
	if len(resources) != 3 {
		t.Fatalf("expected 3 resources in deletion order, got %d", len(resources))
	}
	// Deletion order: CodeBuild project → Security Group → IAM Role
	if resources[0].TypeName != "AWS::CodeBuild::Project" || resources[0].Identifier != "fabrica-ci" {
		t.Errorf("first resource = %+v, want CodeBuild project", resources[0])
	}
	if resources[1].TypeName != "AWS::EC2::SecurityGroup" || resources[1].Identifier != "fabrica-ci-sg" {
		t.Errorf("second resource = %+v, want SecurityGroup", resources[1])
	}
	if resources[2].TypeName != "AWS::IAM::Role" || resources[2].Identifier != "fabrica-ci-codebuild" {
		t.Errorf("third resource = %+v, want IAM role", resources[2])
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
