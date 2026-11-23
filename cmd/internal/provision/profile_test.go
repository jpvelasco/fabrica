package provision

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/instanceprofile"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestExecuteStepExternalReferencesWithoutCreate(t *testing.T) {
	var out bytes.Buffer
	create := func(context.Context, *cloud.Resource) error { t.Error("create called"); return nil }
	step := CreateStep{Label: "Instance profile", TypeName: cloud.TypeAWSIAMInstanceProfile, ExternalID: "workload"}
	resources, err := ExecuteStep(context.Background(), step, "perforce", "v1", "provisioning", nil, fabricastate.NewState("", ""), &out, create, func(*fabricastate.State) error { return nil })
	if err != nil || len(resources) != 1 || !instanceprofile.IsExternal(resources[0]) || !strings.Contains(out.String(), "pre-created, not managed") {
		t.Errorf("ExecuteStep = %+v, %v, output %q; want one external record and a pre-created note", resources, err, out.String())
	}
}

// VerifyProfile fails closed for a provider that cannot read profiles.
func TestVerifyProfileRefusesProviderWithoutReader(t *testing.T) {
	err := VerifyProfile(context.Background(), struct{ cloud.Provider }{}, instanceprofile.Ref{Name: "workload"}, "k")
	if err == nil || !strings.Contains(err.Error(), "refusing to create") {
		t.Errorf("VerifyProfile(no inspector) = %v, want a refusal", err)
	}
	if got := ProfileLines(instanceprofile.Ref{Name: "workload"}, "IAM Role: r", "Instance Profile: workload"); len(got) != 1 || !strings.HasSuffix(got[0], "(pre-created, not managed)") {
		t.Errorf("ProfileLines(external) = %v", got)
	}
}
