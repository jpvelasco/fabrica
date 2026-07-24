package provision

import (
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
