package provision

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

type workflowTestPlan struct {
	name string
}

func TestRunCreate_DryRunStopsBeforeStateAccess(t *testing.T) {
	var events []string
	spec := workflowTestSpec(&events)
	spec.DryRun = true

	if err := RunCreate(context.Background(), spec); err != nil {
		t.Fatalf("RunCreate() error = %v", err)
	}
	if want := []string{"dry-run"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRunCreate_ReadStateError(t *testing.T) {
	wantErr := errors.New("state unavailable")
	var events []string
	spec := workflowTestSpec(&events)
	spec.ReadState = func() (*fabricastate.State, error) {
		events = append(events, "read-state")
		return nil, wantErr
	}

	err := RunCreate(context.Background(), spec)
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunCreate() error = %v, want wrapped %v", err, wantErr)
	}
	if !strings.Contains(err.Error(), "reading state") {
		t.Fatalf("RunCreate() error = %q, want state context", err)
	}
	if want := []string{"read-state"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRunCreate_ExistingModuleStopsBeforePlanAndConfirmation(t *testing.T) {
	var events []string
	spec := workflowTestSpec(&events)
	spec.ExistingMessage = "already provisioned\nremove it first\n"
	st := fabricastate.NewState("123456789012", "us-west-2")
	st.UpsertModule(spec.ModuleName, "1", "ready", nil)
	spec.ReadState = func() (*fabricastate.State, error) {
		events = append(events, "read-state")
		return st, nil
	}

	if err := RunCreate(context.Background(), spec); err != nil {
		t.Fatalf("RunCreate() error = %v", err)
	}
	if want := []string{"read-state"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if got := spec.Out.(*bytes.Buffer).String(); got != spec.ExistingMessage {
		t.Fatalf("output = %q, want %q", got, spec.ExistingMessage)
	}
}

func TestRunCreate_RejectedConfirmationStopsBeforeApply(t *testing.T) {
	var events []string
	spec := workflowTestSpec(&events)
	spec.Confirm = func(_, _ string) bool {
		events = append(events, "confirm")
		return false
	}

	if err := RunCreate(context.Background(), spec); err != nil {
		t.Fatalf("RunCreate() error = %v", err)
	}
	if want := []string{"read-state", "apply-plan", "confirm"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if got := spec.Out.(*bytes.Buffer).String(); !strings.Contains(got, "Cancelled. No AWS calls were made.") {
		t.Fatalf("output = %q, want cancellation message", got)
	}
}

func TestRunCreate_HappyPathOrderAndArguments(t *testing.T) {
	type contextKey string
	const key contextKey = "test"
	ctx := context.WithValue(context.Background(), key, "value")

	var events []string
	spec := workflowTestSpec(&events)
	wantState := fabricastate.NewState("123456789012", "us-west-2")
	spec.ReadState = func() (*fabricastate.State, error) {
		events = append(events, "read-state")
		return wantState, nil
	}
	spec.Apply = func(gotCtx context.Context, gotState *fabricastate.State, gotPlan workflowTestPlan) error {
		events = append(events, "apply")
		if gotCtx.Value(key) != "value" {
			t.Error("Apply() did not receive caller context")
		}
		if gotState != wantState {
			t.Error("Apply() did not receive state returned by ReadState")
		}
		if gotPlan != spec.Plan {
			t.Errorf("Apply() plan = %#v, want %#v", gotPlan, spec.Plan)
		}
		return nil
	}

	if err := RunCreate(ctx, spec); err != nil {
		t.Fatalf("RunCreate() error = %v", err)
	}
	if want := []string{"read-state", "apply-plan", "confirm", "apply"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestRunCreate_AssumeYesBypassesConfirmerAndReturnsApplyError(t *testing.T) {
	wantErr := errors.New("create failed")
	var events []string
	spec := workflowTestSpec(&events)
	spec.AssumeYes = true
	spec.Confirm = func(_, _ string) bool {
		t.Fatal("Confirm called with AssumeYes")
		return false
	}
	spec.Apply = func(context.Context, *fabricastate.State, workflowTestPlan) error {
		events = append(events, "apply")
		return wantErr
	}

	err := RunCreate(context.Background(), spec)
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunCreate() error = %v, want %v", err, wantErr)
	}
	if want := []string{"read-state", "apply-plan", "apply"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if got := spec.Out.(*bytes.Buffer).String(); !strings.Contains(got, "--yes flag set") {
		t.Fatalf("output = %q, want confirmation bypass message", got)
	}
}

func workflowTestSpec(events *[]string) CreateSpec[workflowTestPlan] {
	return CreateSpec[workflowTestPlan]{
		ModuleName:      "horde",
		Account:         "123456789012",
		Plan:            workflowTestPlan{name: "plan"},
		Out:             &bytes.Buffer{},
		ExistingMessage: "already provisioned\n",
		Confirm: func(_, _ string) bool {
			*events = append(*events, "confirm")
			return true
		},
		ReadState: func() (*fabricastate.State, error) {
			*events = append(*events, "read-state")
			return fabricastate.NewState("123456789012", "us-west-2"), nil
		},
		PrintDryRun: func(workflowTestPlan) {
			*events = append(*events, "dry-run")
		},
		PrintApplyPlan: func(workflowTestPlan) {
			*events = append(*events, "apply-plan")
		},
		Apply: func(context.Context, *fabricastate.State, workflowTestPlan) error {
			*events = append(*events, "apply")
			return nil
		},
	}
}
