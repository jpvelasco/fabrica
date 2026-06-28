package setup

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	fabricacost "github.com/jpvelasco/fabrica/internal/cost"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }
func (fakeProvider) Identity(context.Context) (string, string, string, error) {
	return "123456789012", "arn", "us-west-2", nil
}
func (fakeProvider) Resources() cloud.ResourceClient { return nil }

func testRuntime() globals.Runtime {
	cfg := config.Defaults()
	cfg.Cloud.AWS.AccountID = "123456789012"
	return globals.Runtime{Provider: fakeProvider{}, Config: cfg}
}

// newCmd builds a command with in-memory seams. createdTypes records the
// TypeName of every resource "created".
func newCmd(out *bytes.Buffer, createErr error, confirmResult bool) (*command, *[]string) {
	created := &[]string{}
	st := fabricastate.NewState("123456789012", "us-west-2")
	c := &command{
		runtime:    testRuntime(),
		out:        out,
		costs:      fabricacost.Global,
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(*fabricastate.State) error { return nil },
		createResource: func(_ context.Context, r *cloud.Resource) error {
			if createErr != nil {
				return createErr
			}
			*created = append(*created, r.TypeName)
			r.Identifier = r.TypeName + "-id"
			return nil
		},
		ensureProject: func(_ context.Context, spec cloud.CodeBuildProjectSpec) (bool, error) {
			if createErr != nil {
				return false, createErr
			}
			*created = append(*created, "AWS::CodeBuild::Project")
			return true, nil
		},
		confirm: func(string) bool { return confirmResult },
	}
	return c, created
}

func TestSetupConfirmYesCreatesBoth(t *testing.T) {
	var out bytes.Buffer
	c, created := newCmd(&out, nil, true)
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(*created) != 2 {
		t.Fatalf("created %d resources, want 2 (%v)", len(*created), *created)
	}
	if (*created)[0] != "AWS::IAM::Role" || (*created)[1] != "AWS::CodeBuild::Project" {
		t.Errorf("creation order = %v, want role then project", *created)
	}
	if !strings.Contains(out.String(), "CI setup complete") {
		t.Errorf("missing completion message:\n%s", out.String())
	}
}

func TestSetupConfirmNoCancels(t *testing.T) {
	var out bytes.Buffer
	c, created := newCmd(&out, nil, false)
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(*created) != 0 {
		t.Errorf("nothing should be created when declined, got %v", *created)
	}
	if !strings.Contains(out.String(), "cancelled") {
		t.Errorf("expected cancellation message:\n%s", out.String())
	}
}

func TestSetupAssumeYesSkipsConfirm(t *testing.T) {
	var out bytes.Buffer
	c, created := newCmd(&out, nil, false) // confirm returns false, but assumeYes bypasses it
	c.assumeYes = true
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(*created) != 2 {
		t.Errorf("assumeYes should create resources, got %v", *created)
	}
}

func TestSetupDryRunCreatesNothing(t *testing.T) {
	var out bytes.Buffer
	c, created := newCmd(&out, nil, true)
	c.dryRun = true
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(*created) != 0 {
		t.Errorf("dry-run must not create resources, got %v", *created)
	}
	if !strings.Contains(out.String(), "dry run") || !strings.Contains(out.String(), "Cost estimate") {
		t.Errorf("dry-run should show plan + cost:\n%s", out.String())
	}
}

func TestSetupCreateErrorPropagates(t *testing.T) {
	var out bytes.Buffer
	c, _ := newCmd(&out, errors.New("AccessDenied"), true)
	if err := c.run(context.Background()); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestSetupIdempotentSkipsExisting(t *testing.T) {
	var out bytes.Buffer
	created := &[]string{}
	st := fabricastate.NewState("123456789012", "us-west-2")
	// Pre-seed both resources as already present.
	st.UpsertModule(moduleName, "fabrica-ci", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-ci-codebuild"},
		{TypeName: "AWS::CodeBuild::Project", Identifier: "fabrica-ci"},
	})
	c := &command{
		runtime:    testRuntime(),
		out:        &out,
		costs:      fabricacost.Global,
		assumeYes:  true,
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(*fabricastate.State) error { return nil },
		createResource: func(_ context.Context, r *cloud.Resource) error {
			*created = append(*created, r.TypeName)
			return nil
		},
		// Project already exists in AWS → EnsureProject reports not-created.
		ensureProject: func(_ context.Context, _ cloud.CodeBuildProjectSpec) (bool, error) {
			return false, nil
		},
		confirm: func(string) bool { return true },
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(*created) != 0 {
		t.Errorf("idempotent run should create nothing via Cloud Control, got %v", *created)
	}
	if !strings.Contains(out.String(), "already exists") {
		t.Errorf("expected skip messages:\n%s", out.String())
	}
}
