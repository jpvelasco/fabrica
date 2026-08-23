package provision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestExecuteStepHappyPath(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	var resources []fabricastate.ModuleResource
	var writtenStates []*fabricastate.State
	writeState := func(s *fabricastate.State) error {
		sCopy := *s
		writtenStates = append(writtenStates, &sCopy)
		return nil
	}
	createResource := func(_ context.Context, r *cloud.Resource) error {
		r.Identifier = "sg-12345"
		return nil
	}

	step := CreateStep{
		Label:             "security group",
		TypeName:          "AWS::EC2::SecurityGroup",
		BuildDesiredState: func() ([]byte, error) { return json.Marshal(map[string]any{"groupName": "test"}) },
	}
	resources, err := ExecuteStep(context.Background(), step, "test-module", "v1", "provisioning", resources, st, &testWriter{}, createResource, writeState)
	if err != nil {
		t.Fatalf("ExecuteStep: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	if resources[0].Identifier != "sg-12345" {
		t.Fatalf("expected sg-12345, got %s", resources[0].Identifier)
	}
	if len(writtenStates) != 1 {
		t.Fatalf("expected 1 state write, got %d", len(writtenStates))
	}
}

func TestExecuteStepReusesExistingResource(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	existing := fabricastate.ModuleResource{
		TypeName:   "AWS::IAM::Role",
		Identifier: "existing-role",
		Properties: map[string]string{"owner": "test"},
	}
	st.UpsertModule("test-module", "v1", "provisioning", []fabricastate.ModuleResource{existing})

	var out bytes.Buffer
	step := CreateStep{
		Label:         "IAM role",
		TypeName:      "AWS::IAM::Role",
		ReuseExisting: true,
		BuildDesiredState: func() ([]byte, error) {
			t.Fatal("BuildDesiredState called for existing resource")
			return nil, nil
		},
	}
	resources, err := ExecuteStep(
		context.Background(), step, "test-module", "v1", "provisioning", nil, st, &out,
		func(context.Context, *cloud.Resource) error {
			t.Fatal("createResource called for existing resource")
			return nil
		},
		func(*fabricastate.State) error {
			t.Fatal("writeState called for existing resource")
			return nil
		},
	)
	if err != nil {
		t.Fatalf("ExecuteStep: %v", err)
	}
	if len(resources) != 1 || resources[0].Identifier != existing.Identifier {
		t.Fatalf("resources = %+v, want existing resource", resources)
	}
	if resources[0].Properties["owner"] != "test" {
		t.Fatalf("existing properties were not preserved: %+v", resources[0].Properties)
	}
	if !strings.Contains(out.String(), "IAM role already exists — skipping: existing-role") {
		t.Fatalf("missing reuse message: %q", out.String())
	}
}

func TestExecuteStepCreatesWhenReusableResourceIsMissing(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	created, written := false, false
	step := CreateStep{
		Label:             "IAM role",
		TypeName:          "AWS::IAM::Role",
		ReuseExisting:     true,
		BuildDesiredState: func() ([]byte, error) { return json.Marshal(map[string]any{}) },
	}
	resources, err := ExecuteStep(
		context.Background(), step, "test-module", "v1", "provisioning", nil, st, &testWriter{},
		func(_ context.Context, r *cloud.Resource) error {
			created = true
			r.Identifier = "new-role"
			return nil
		},
		func(*fabricastate.State) error {
			written = true
			return nil
		},
	)
	if err != nil {
		t.Fatalf("ExecuteStep: %v", err)
	}
	if !created || !written {
		t.Fatalf("created = %t, written = %t; want both true", created, written)
	}
	if len(resources) != 1 || resources[0].Identifier != "new-role" {
		t.Fatalf("resources = %+v, want newly created role", resources)
	}
}

func TestExecuteStepBuildDesiredStateError(t *testing.T) {
	step := CreateStep{
		Label:             "security group",
		TypeName:          "AWS::EC2::SecurityGroup",
		BuildDesiredState: func() ([]byte, error) { return nil, errors.New("build failed") },
	}
	_, err := ExecuteStep(context.Background(), step, "m", "v", "s", nil, fabricastate.NewState("", ""), &testWriter{}, func(context.Context, *cloud.Resource) error { return nil }, func(*fabricastate.State) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "build failed") {
		t.Fatalf("expected build error, got: %v", err)
	}
}

func TestExecuteStepCreateError(t *testing.T) {
	step := CreateStep{
		Label:             "instance",
		TypeName:          "AWS::EC2::Instance",
		BuildDesiredState: func() ([]byte, error) { return json.Marshal(map[string]any{}) },
	}
	_, err := ExecuteStep(context.Background(), step, "m", "v", "s", nil, fabricastate.NewState("", ""), &testWriter{}, func(context.Context, *cloud.Resource) error { return errors.New("create failed") }, func(*fabricastate.State) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "create failed") {
		t.Fatalf("expected create error, got: %v", err)
	}
}

func TestExecuteStepWriteStateErrorReturns(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	step := CreateStep{
		Label:             "instance",
		TypeName:          "AWS::EC2::Instance",
		BuildDesiredState: func() ([]byte, error) { return json.Marshal(map[string]any{}) },
	}
	_, err := ExecuteStep(context.Background(), step, "m", "v", "s", nil, st, &testWriter{}, func(context.Context, *cloud.Resource) error { return nil }, func(*fabricastate.State) error { return errors.New("disk full") })
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("expected write state error, got: %v", err)
	}
}

func TestExecuteStepWriteStateIgnoreError(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	step := CreateStep{
		Label:             "security group",
		TypeName:          "AWS::EC2::SecurityGroup",
		BuildDesiredState: func() ([]byte, error) { return json.Marshal(map[string]any{}) },
		IgnoreWriteError:  true,
	}
	resources, err := ExecuteStep(context.Background(), step, "m", "v", "s", nil, st, &testWriter{}, func(context.Context, *cloud.Resource) error { return nil }, func(*fabricastate.State) error { return errors.New("disk full") })
	if err != nil {
		t.Fatalf("expected no error when IgnoreWriteError is true, got: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource despite write error, got %d", len(resources))
	}
}

func TestExecuteStepResourceIdentifierOverride(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	step := CreateStep{
		Label:             "instance profile",
		TypeName:          "AWS::IAM::InstanceProfile",
		BuildDesiredState: func() ([]byte, error) { return json.Marshal(map[string]any{}) },
		ResourceIdentifier: func(created *cloud.Resource) string {
			return "custom-profile-name"
		},
	}
	resources, err := ExecuteStep(context.Background(), step, "m", "v", "s", nil, st, &testWriter{}, func(ctx context.Context, r *cloud.Resource) error {
		r.Identifier = "arn:aws:iam::123:instance-profile/test"
		return nil
	}, func(*fabricastate.State) error { return nil })
	if err != nil {
		t.Fatalf("ExecuteStep: %v", err)
	}
	if resources[0].Identifier != "custom-profile-name" {
		t.Fatalf("expected custom-profile-name, got %s", resources[0].Identifier)
	}
}

func TestExecuteStepProperties(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	step := CreateStep{
		Label:             "instance",
		TypeName:          "AWS::EC2::Instance",
		BuildDesiredState: func() ([]byte, error) { return json.Marshal(map[string]any{}) },
		Properties:        map[string]string{"instanceType": "m5.xlarge", "volumeSize": "500"},
	}
	resources, err := ExecuteStep(context.Background(), step, "m", "v", "s", nil, st, &testWriter{}, func(context.Context, *cloud.Resource) error { return nil }, func(*fabricastate.State) error { return nil })
	if err != nil {
		t.Fatalf("ExecuteStep: %v", err)
	}
	if resources[0].Properties["instanceType"] != "m5.xlarge" {
		t.Fatalf("expected m5.xlarge, got %s", resources[0].Properties["instanceType"])
	}
}

type testWriter struct{}

func (w *testWriter) Write(p []byte) (n int, err error) { return len(p), nil }

// TestExecuteStepMultipleStepsInSequence verifies that multiple ExecuteStep
// calls correctly accumulate resources across steps.
func TestExecuteStepMultipleStepsInSequence(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	var resources []fabricastate.ModuleResource
	writeState := func(*fabricastate.State) error { return nil }
	createCounter := 0
	createResource := func(_ context.Context, r *cloud.Resource) error {
		createCounter++
		r.Identifier = fmt.Sprintf("%s-%d", r.TypeName, createCounter)
		return nil
	}

	steps := []CreateStep{
		{Label: "security group", TypeName: "AWS::EC2::SecurityGroup", BuildDesiredState: func() ([]byte, error) { return json.Marshal(map[string]any{}) }},
		{Label: "instance", TypeName: "AWS::EC2::Instance", BuildDesiredState: func() ([]byte, error) { return json.Marshal(map[string]any{}) }},
	}

	for _, step := range steps {
		var err error
		resources, err = ExecuteStep(context.Background(), step, "m", "v", "s", resources, st, &testWriter{}, createResource, writeState)
		if err != nil {
			t.Fatalf("ExecuteStep: %v", err)
		}
	}
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(resources))
	}
	if resources[0].TypeName != "AWS::EC2::SecurityGroup" {
		t.Fatalf("expected SG, got %s", resources[0].TypeName)
	}
	if resources[1].TypeName != "AWS::EC2::Instance" {
		t.Fatalf("expected Instance, got %s", resources[1].TypeName)
	}
}

// TestExecuteStepPostCreate verifies PostCreate runs after a fresh create and
// is skipped on the reuse path; a PostCreate error fails the step.
func TestExecuteStepPostCreate(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	var postIDs []string
	newStep := func(postErr error) CreateStep {
		return CreateStep{
			Label:             "instance",
			TypeName:          "AWS::EC2::Instance",
			BuildDesiredState: func() ([]byte, error) { return []byte(`{}`), nil },
			PostCreate: func(_ context.Context, id string) error {
				postIDs = append(postIDs, id)
				return postErr
			},
		}
	}
	create := func(_ context.Context, r *cloud.Resource) error { r.Identifier = "i-new"; return nil }
	write := func(*fabricastate.State) error { return nil }
	var out bytes.Buffer

	if _, err := ExecuteStep(context.Background(), newStep(nil), "m", "v", "provisioning", nil, st, &out, create, write); err != nil {
		t.Fatalf("fresh create with PostCreate: %v", err)
	}
	if len(postIDs) != 1 || postIDs[0] != "i-new" {
		t.Fatalf("postCreate ids = %v, want [i-new]", postIDs)
	}

	// Reuse path must not invoke PostCreate.
	existing := fabricastate.ModuleResource{TypeName: "AWS::EC2::Instance", Identifier: "i-old"}
	st.UpsertModule("m", "v", "provisioning", []fabricastate.ModuleResource{existing})
	reuseStep := newStep(nil)
	reuseStep.ReuseExisting = true
	before := len(postIDs)
	if _, err := ExecuteStep(context.Background(), reuseStep, "m", "v", "provisioning", nil, st, &out, create, write); err != nil {
		t.Fatalf("reuse path: %v", err)
	}
	if len(postIDs) != before {
		t.Error("PostCreate ran on the reuse path; it must only run for fresh creates")
	}

	// PostCreate error fails the step.
	_, postErr := ExecuteStep(context.Background(), newStep(errors.New("tag boom")), "m2", "v", "provisioning", nil, fabricastate.NewState("123456789012", "us-east-1"), &out, create, write)
	if postErr == nil || !strings.Contains(postErr.Error(), "post-create") {
		t.Errorf("err = %v, want wrapped post-create failure", postErr)
	}
}
