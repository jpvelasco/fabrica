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

// profileProvider is a provider that can read instance profiles.
type profileProvider struct {
	cloud.Provider
	info cloud.InstanceProfileInfo
	err  error
}

func (p *profileProvider) GetInstanceProfile(context.Context, string) (cloud.InstanceProfileInfo, error) {
	return p.info, p.err
}

func TestExecuteStepExternalReferencesWithoutCreate(t *testing.T) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	create := func(context.Context, *cloud.Resource) error {
		t.Error("create called for a referenced profile")
		return nil
	}
	var out bytes.Buffer
	step := CreateStep{Label: "Instance profile", TypeName: cloud.TypeAWSIAMInstanceProfile, ExternalID: "workload"}
	resources, err := ExecuteStep(context.Background(), step, "perforce", "v1", "provisioning", nil, st, &out, create, func(*fabricastate.State) error { return nil })
	if err != nil {
		t.Fatalf("ExecuteStep: %v", err)
	}
	if len(resources) != 1 || resources[0].Identifier != "workload" || !instanceprofile.IsExternal(resources[0]) {
		t.Errorf("resources = %+v, want one external profile named workload", resources)
	}
	if !strings.Contains(out.String(), "pre-created, not managed") {
		t.Errorf("output = %q, want it to say the profile is pre-created", out.String())
	}
}

func TestVerifyProfile(t *testing.T) {
	ref := instanceprofile.Ref{Name: "workload"}
	if err := VerifyProfile(context.Background(), nil, instanceprofile.Ref{}, "perforce.instanceProfile"); err != nil {
		t.Errorf("VerifyProfile(managed) = %v, want no check", err)
	}
	if err := VerifyProfile(context.Background(), &profileProvider{info: cloud.InstanceProfileInfo{Roles: []string{"r"}}}, ref, "perforce.instanceProfile"); err != nil {
		t.Errorf("VerifyProfile(good) = %v", err)
	}
	err := VerifyProfile(context.Background(), &profileProvider{err: cloud.ErrResourceNotFound}, ref, "perforce.instanceProfile")
	if err == nil || !strings.Contains(err.Error(), "workload") {
		t.Errorf("VerifyProfile(missing) = %v, want an error naming the profile", err)
	}
	// A provider that cannot read profiles fails closed.
	err = VerifyProfile(context.Background(), struct{ cloud.Provider }{}, ref, "perforce.instanceProfile")
	if err == nil || !strings.Contains(err.Error(), "refusing to create") {
		t.Errorf("VerifyProfile(no inspector) = %v, want a refusal", err)
	}
}

func TestProfileLines(t *testing.T) {
	if got := ProfileLines(instanceprofile.Ref{Name: "p"}, "IAM Role: r", "Instance Profile: p"); len(got) != 1 || got[0] != "Instance Profile: p (pre-created, not managed)" {
		t.Errorf("external ProfileLines = %v, want one not-managed row", got)
	}
	if got := ProfileLines(instanceprofile.Ref{}, "IAM Role: r", "Instance Profile: p"); len(got) != 2 {
		t.Errorf("managed ProfileLines = %v, want role and profile rows", got)
	}
}
