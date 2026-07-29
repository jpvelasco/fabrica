package destroy

import (
	"io"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/teardown"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestNewTeardownWiring(t *testing.T) {
	rt := globals.Runtime{Config: config.Defaults()}
	tc := NewTeardown(rt, io.Discard)

	if tc.Spec.ModuleName != "horde" {
		t.Errorf("ModuleName = %q, want horde", tc.Spec.ModuleName)
	}
	if tc.Spec.Verb != "destroy" {
		t.Errorf("Verb = %q, want destroy", tc.Spec.Verb)
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
}

func TestNewTeardownWithProvider(t *testing.T) {
	rt := globals.Runtime{
		Config:   config.Defaults(),
		Provider: &testutil.TestProvider{},
	}
	tc := NewTeardown(rt, io.Discard)

	if tc.DeleteResource == nil {
		t.Error("DeleteResource must be wired when provider is non-nil")
	}
	if tc.GetResource == nil {
		t.Error("GetResource must be wired when provider is non-nil")
	}
}

func TestNewTeardownSpecStrings(t *testing.T) {
	tc := teardown.Command{Spec: spec}

	if tc.Spec.ModuleName != "horde" {
		t.Errorf("ModuleName = %q, want horde", tc.Spec.ModuleName)
	}
	if tc.Spec.Verb != "destroy" {
		t.Errorf("Verb = %q, want destroy", tc.Spec.Verb)
	}
	if tc.Spec.VersionLabel != "AMI ID" {
		t.Errorf("VersionLabel = %q, want AMI ID", tc.Spec.VersionLabel)
	}
	if tc.Spec.Title != "Unreal Horde build coordinator" {
		t.Errorf("Title = %q, want Unreal Horde build coordinator", tc.Spec.Title)
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
