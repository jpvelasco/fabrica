package perforce

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/instanceprofile"
	"github.com/jpvelasco/fabrica/internal/naming"
)

func TestNewCreatePlanReferencedProfileUsesARNName(t *testing.T) {
	cfg := config.PerforceConfig{InstanceProfile: "arn:aws:iam::123456789012:instance-profile/platform/workload"}
	plan, err := NewCreatePlan(context.Background(), naming.Default, cfg, "123456789012", "us-east-1", DefaultHelixVersion, nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	if !plan.Profile.External() || plan.Profile.Name != "workload" || plan.InstanceProfileName != "workload" {
		t.Errorf("plan profile = %+v, InstanceProfileName = %q; want external workload", plan.Profile, plan.InstanceProfileName)
	}

	cfg.InstanceProfile = "arn:aws:iam::123456789012:role/workload"
	if _, err := NewCreatePlan(context.Background(), naming.Default, cfg, "123456789012", "us-east-1", DefaultHelixVersion, nil); err == nil || !strings.Contains(err.Error(), "instanceProfile") {
		t.Errorf("NewCreatePlan(role ARN) error = %v, want an instanceProfile error", err)
	}
}

func TestRequiredResourcesReferencedProfileOmitsRole(t *testing.T) {
	p := CreatePlan{Profile: instanceprofile.Ref{Name: "workload"}}
	want := []string{cloud.TypeAWSEC2SecurityGroup, cloud.TypeAWSIAMInstanceProfile, cloud.TypeAWSEC2Instance}
	if got := p.RequiredResources(); !reflect.DeepEqual(got, want) {
		t.Errorf("RequiredResources() = %v, want %v", got, want)
	}
}
