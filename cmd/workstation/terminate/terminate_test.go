package terminate

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/workstation"
)

func TestNewTeardownWiring(t *testing.T) {
	rt := globals.Runtime{Config: config.Defaults()}
	tc := NewTeardown(rt, io.Discard)

	if tc.Spec.ModuleName != "workstation" {
		t.Errorf("ModuleName = %q, want workstation", tc.Spec.ModuleName)
	}
	if tc.Spec.Verb != "terminate" {
		t.Errorf("Verb = %q, want terminate", tc.Spec.Verb)
	}
	if !tc.SkipConfirm {
		t.Error("SkipConfirm must be true (set by shared NewTeardown)")
	}
	if !tc.AssumeYes {
		t.Error("AssumeYes must be true (set by shared NewTeardown)")
	}
	if tc.ReadState == nil {
		t.Error("ReadState must be wired")
	}
	if tc.WriteState == nil {
		t.Error("WriteState must be wired")
	}
	if tc.Confirm == nil {
		t.Error("Confirm must be wired")
	}
	// Without a provider, delete seams are nil.
	if tc.DeleteResource != nil {
		t.Error("DeleteResource must be nil when provider is nil")
	}
	if tc.GetResource != nil {
		t.Error("GetResource must be nil when provider is nil")
	}
	if tc.SDKDeleteFunc != nil {
		t.Error("SDKDeleteFunc must be nil when provider is nil")
	}
}

func TestNewTeardownWithProvider(t *testing.T) {
	provider := &testutil.TestProvider{}
	rt := globals.Runtime{
		Config:   config.Defaults(),
		Provider: provider,
	}
	tc := NewTeardown(rt, io.Discard)

	if tc.DeleteResource == nil {
		t.Error("DeleteResource must be wired when provider is non-nil")
	}
	if tc.GetResource == nil {
		t.Error("GetResource must be wired when provider is non-nil")
	}
	if tc.SDKDeleteFunc == nil {
		t.Fatal("SDKDeleteFunc must delete the session-password parameter")
	}
	if err := tc.SDKDeleteFunc(context.Background(), cloud.TypeAWSEC2Instance, "i-1"); !errors.Is(err, cloud.ErrNotHandled) {
		t.Fatalf("instance delete = %v, want ErrNotHandled", err)
	}
	if len(provider.DeletedParameters) != 0 {
		t.Fatalf("non-parameter delete removed %v", provider.DeletedParameters)
	}
	if err := tc.SDKDeleteFunc(context.Background(), cloud.TypeAWSSSMParameter, workstation.SessionPasswordParameter); err != nil {
		t.Fatal(err)
	}
	if len(provider.DeletedParameters) != 1 || provider.DeletedParameters[0] != workstation.SessionPasswordParameter {
		t.Fatalf("DeletedParameters = %v", provider.DeletedParameters)
	}
}

func TestNewTeardownSpecStrings(t *testing.T) {
	tc := teardown.Command{Spec: spec}

	if tc.Spec.ModuleName != "workstation" {
		t.Errorf("ModuleName = %q, want workstation", tc.Spec.ModuleName)
	}
	if tc.Spec.Verb != "terminate" {
		t.Errorf("Verb = %q, want terminate", tc.Spec.Verb)
	}
	if tc.Spec.VersionLabel != "AMI ID" {
		t.Errorf("VersionLabel = %q, want AMI ID", tc.Spec.VersionLabel)
	}
	if tc.Spec.Title != "Cloud Workstation" {
		t.Errorf("Title = %q, want Cloud Workstation", tc.Spec.Title)
	}
	for _, field := range []struct{ name, value string }{
		{"NotProvisioned", tc.Spec.NotProvisioned},
		{"PlanHeader", tc.Spec.PlanHeader},
		{"DryRunHeader", tc.Spec.DryRunHeader},
		{"Irreversible", tc.Spec.Irreversible},
		{"SuccessMessage", tc.Spec.SuccessMessage},
	} {
		if field.value == "" {
			t.Errorf("%s must not be empty", field.name)
		}
	}
}
