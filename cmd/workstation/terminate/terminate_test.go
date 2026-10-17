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
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/workstation"
)

// testWSParamName is a per-create session-password parameter name (see
// workstation.SessionPasswordParameterFor); cobra_test.go keeps its own
// copy because the two packages cannot share constants.
const testWSParamName = "/fabrica/workstation/abc123def4567890abc123def4567890/dcv-password"

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
	// The recorded name is a per-create name (instance ID as nonce); the
	// SDK delete removes exactly that name.
	recorded, err := workstation.SessionPasswordParameterFor("i-0123456789abcdef0")
	if err != nil {
		t.Fatal(err)
	}
	if err := tc.SDKDeleteFunc(context.Background(), cloud.TypeAWSSSMParameter, recorded); err != nil {
		t.Fatal(err)
	}
	if len(provider.DeletedParameters) != 1 || provider.DeletedParameters[0] != recorded {
		t.Fatalf("DeletedParameters = %v, want %q", provider.DeletedParameters, recorded)
	}
}

// TestResourceOrderSkipsParameterWhenNotRecorded covers pre-#498 state (no
// sessionPasswordParameter on the instance): no AWS::SSM::Parameter entry is
// emitted, so terminate never touches a name it does not own.
func TestResourceOrderSkipsParameterWhenNotRecorded(t *testing.T) {
	m := &fabricastate.ModuleState{
		Name:   "workstation",
		Status: "ready",
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-old"},
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-old"},
		},
	}
	order := workstationResourceOrder(m)
	for _, r := range order {
		if r.TypeName == cloud.TypeAWSSSMParameter {
			t.Fatalf("order includes an SSM parameter the instance never recorded: %+v", order)
		}
	}
	if len(order) != 2 {
		t.Fatalf("order = %+v, want instance + SG only", order)
	}
}

// TestResourceOrderUsesRecordedParameterName pins the happy path: the SSM
// parameter entry carries exactly the name recorded in state.
func TestResourceOrderUsesRecordedParameterName(t *testing.T) {
	m := &fabricastate.ModuleState{
		Name:   "workstation",
		Status: "ready",
		Resources: []fabricastate.ModuleResource{
			{TypeName: "AWS::EC2::Instance", Identifier: "i-1", Properties: map[string]string{"sessionPasswordParameter": testWSParamName}},
		},
	}
	order := workstationResourceOrder(m)
	found := ""
	for _, r := range order {
		if r.TypeName == cloud.TypeAWSSSMParameter {
			found = r.Identifier
		}
	}
	if found != testWSParamName {
		t.Fatalf("parameter identifier = %q, want %q", found, testWSParamName)
	}
}

// TestResourceOrderSkipsMalformedParameterName pins the guard against a
// hand-edited or corrupted state entry: a recorded sessionPasswordParameter
// that is not a well-formed per-create name must be skipped (with a warning)
// instead of deleting an arbitrary SSM parameter.
func TestResourceOrderSkipsMalformedParameterName(t *testing.T) {
	for _, bad := range []string{
		"/fabrica/workstation/a/b/dcv-password",             // nested segment in the nonce
		"fabrica/workstation/abc123def4567890/dcv-password", // missing leading slash
		"/fabrica/perforce/abc123def4567890/dcv-password",   // wrong prefix
		"/fabrica/workstation/abc123def4567890/dcv-secret",  // wrong leaf
		"/fabrica/workstation/dcv-password",                 // missing nonce segment
	} {
		t.Run(bad, func(t *testing.T) {
			m := &fabricastate.ModuleState{
				Name:   "workstation",
				Status: "ready",
				Resources: []fabricastate.ModuleResource{
					{TypeName: "AWS::EC2::Instance", Identifier: "i-1", Properties: map[string]string{"sessionPasswordParameter": bad}},
				},
			}
			order := workstationResourceOrder(m)
			for _, r := range order {
				if r.TypeName == cloud.TypeAWSSSMParameter {
					t.Fatalf("order includes the malformed SSM parameter name %q: %+v", bad, order)
				}
			}
			if len(order) != 1 {
				t.Fatalf("order = %+v, want the instance only", order)
			}
		})
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
