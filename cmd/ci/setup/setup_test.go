package setup

import (
	"bytes"
	"context"
	"encoding/json"
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

// TestSetupEnsureProjectError verifies error propagates when EnsureProject fails.
func TestSetupEnsureProjectError(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-west-2")
	c := &command{
		runtime:    testRuntime(),
		out:        &out,
		costs:      fabricacost.Global,
		assumeYes:  true,
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(*fabricastate.State) error { return nil },
		createResource: func(_ context.Context, r *cloud.Resource) error {
			r.Identifier = r.TypeName + "-id"
			return nil
		},
		ensureProject: func(_ context.Context, _ cloud.CodeBuildProjectSpec) (bool, error) {
			return false, errors.New("service unavailable")
		},
		confirm: func(string) bool { return true },
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when EnsureProject fails")
	}
	if !strings.Contains(err.Error(), "CodeBuild project") {
		t.Errorf("expected CodeBuild error, got: %v", err)
	}
}

// TestSetupWriteStateError verifies error propagates when writeState fails.
func TestSetupWriteStateError(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-west-2")
	writeErr := errors.New("disk full")
	c := &command{
		runtime:    testRuntime(),
		out:        &out,
		costs:      fabricacost.Global,
		assumeYes:  true,
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(*fabricastate.State) error { return writeErr },
		createResource: func(_ context.Context, r *cloud.Resource) error {
			r.Identifier = r.TypeName + "-id"
			return nil
		},
		ensureProject: func(_ context.Context, _ cloud.CodeBuildProjectSpec) (bool, error) {
			return true, nil
		},
		confirm: func(string) bool { return true },
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when writeState fails")
	}
	if !strings.Contains(err.Error(), "writing state") {
		t.Errorf("expected writing state error, got: %v", err)
	}
}

// TestSetupDryRunJSON verifies dry-run output contains cost estimate.
func TestSetupDryRunJSON(t *testing.T) {
	var out bytes.Buffer
	c, created := newCmd(&out, nil, true)
	c.dryRun = true
	if err := c.run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(*created) != 0 {
		t.Errorf("dry-run must not create resources, got %v", *created)
	}
	output := out.String()
	if !strings.Contains(output, "dry run") {
		t.Errorf("expected dry run header:\n%s", output)
	}
	if !strings.Contains(output, "Cost estimate") {
		t.Errorf("expected cost estimate:\n%s", output)
	}
}

// TestSetupNoProvider verifies error when provider is nil.
func TestSetupNoProvider(t *testing.T) {
	var out bytes.Buffer
	c := &command{
		runtime: globals.Runtime{Config: config.Defaults(), Provider: nil},
		out:     &out,
		costs:   fabricacost.Global,
	}
	err := c.run(context.Background())
	if err == nil {
		t.Fatal("expected error when provider is nil")
	}
	if !strings.Contains(err.Error(), "no cloud provider") {
		t.Errorf("expected no cloud provider error, got: %v", err)
	}
}

// TestSetupResolveHordeURL verifies that resolveHordeURL returns the Horde URL
// when Horde is provisioned with an accessible instance.
func TestSetupResolveHordeURL(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-west-2")
	st.UpsertModule("horde", "", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-horde"},
		{TypeName: "AWS::EC2::Instance", Identifier: "i-horde123"},
	})
	c := &command{
		runtime:    testRuntime(),
		out:        &out,
		costs:      fabricacost.Global,
		readState:  func() (*fabricastate.State, error) { return st, nil },
		writeState: func(*fabricastate.State) error { return nil },
		getResource: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = json.RawMessage(`{"PrivateIpAddress":"10.0.1.42"}`)
			return nil
		},
	}
	url := c.resolveHordeURL(context.Background())
	if url != "http://10.0.1.42:5000" {
		t.Errorf("resolveHordeURL = %q, want http://10.0.1.42:5000", url)
	}
}

// TestSetupResolveHordeURLNoHorde verifies empty string when Horde not provisioned.
func TestSetupResolveHordeURLNoHorde(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-west-2")
	c := &command{
		runtime:   testRuntime(),
		out:       &out,
		costs:     fabricacost.Global,
		readState: func() (*fabricastate.State, error) { return st, nil },
	}
	url := c.resolveHordeURL(context.Background())
	if url != "" {
		t.Errorf("resolveHordeURL = %q, want empty when horde not provisioned", url)
	}
}

// TestSetupResolveHordeURLGetError verifies empty string when Get fails.
func TestSetupResolveHordeURLGetError(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-west-2")
	st.UpsertModule("horde", "", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-horde123"},
	})
	c := &command{
		runtime:   testRuntime(),
		out:       &out,
		costs:     fabricacost.Global,
		readState: func() (*fabricastate.State, error) { return st, nil },
		getResource: func(_ context.Context, _ *cloud.Resource) error {
			return errors.New("not found")
		},
	}
	url := c.resolveHordeURL(context.Background())
	if url != "" {
		t.Errorf("resolveHordeURL = %q, want empty when Get fails", url)
	}
}

// TestPrivateIPFromActualState verifies the helper extracts IP from JSON.
func TestPrivateIPFromActualState(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{
			name: "valid IP",
			data: []byte(`{"PrivateIpAddress":"10.0.1.42"}`),
			want: "10.0.1.42",
		},
		{
			name: "empty object",
			data: []byte(`{}`),
			want: "",
		},
		{
			name: "nil",
			data: nil,
			want: "",
		},
		{
			name: "invalid JSON",
			data: []byte(`{bad`),
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := privateIPFromActualState(tt.data)
			if got != tt.want {
				t.Errorf("privateIPFromActualState(%q) = %q, want %q", string(tt.data), got, tt.want)
			}
		})
	}
}

// TestSetupResolveHordeURLNoGetResource verifies empty string when getResource is nil.
func TestSetupResolveHordeURLNoGetResource(t *testing.T) {
	var out bytes.Buffer
	st := fabricastate.NewState("123456789012", "us-west-2")
	st.UpsertModule("horde", "", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::Instance", Identifier: "i-horde123"},
	})
	c := &command{
		runtime:   testRuntime(),
		out:       &out,
		costs:     fabricacost.Global,
		readState: func() (*fabricastate.State, error) { return st, nil },
		// getResource is nil — should return empty string
	}
	url := c.resolveHordeURL(context.Background())
	if url != "" {
		t.Errorf("resolveHordeURL = %q, want empty when getResource is nil", url)
	}
}

// TestAppendUnique verifies duplicate resources are not added.
func TestAppendUnique(t *testing.T) {
	resources := []fabricastate.ModuleResource{
		{TypeName: "AWS::IAM::Role", Identifier: "fabrica-ci-codebuild"},
	}

	// Adding a different type should append.
	resources = appendUnique(resources, fabricastate.ModuleResource{
		TypeName:   "AWS::CodeBuild::Project",
		Identifier: "fabrica-ci",
	})
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(resources))
	}

	// Adding a duplicate type should keep the original.
	resources = appendUnique(resources, fabricastate.ModuleResource{
		TypeName:   "AWS::IAM::Role",
		Identifier: "different-role",
	})
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources after duplicate, got %d", len(resources))
	}
	if resources[0].Identifier != "fabrica-ci-codebuild" {
		t.Errorf("first resource should be unchanged, got %s", resources[0].Identifier)
	}
}
