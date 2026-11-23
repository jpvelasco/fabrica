package perforce

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

func TestReferencedProfileInPlan(t *testing.T) {
	cfg := config.PerforceConfig{InstanceProfile: "arn:aws:iam::123456789012:instance-profile/platform/workload"}
	plan, err := NewCreatePlan(context.Background(), naming.Default, cfg, "123456789012", "us-east-1", DefaultHelixVersion, nil)
	if err != nil || !plan.Profile.External() || plan.InstanceProfileName != "workload" {
		t.Fatalf("NewCreatePlan = %+v, %v; want external workload", plan, err)
	}
	// A referenced profile records no role.
	want := []string{cloud.TypeAWSEC2SecurityGroup, cloud.TypeAWSIAMInstanceProfile, cloud.TypeAWSEC2Instance}
	if got := plan.RequiredResources(); !reflect.DeepEqual(got, want) {
		t.Errorf("RequiredResources() = %v, want %v", got, want)
	}
	cfg.InstanceProfile = "arn:aws:iam::123456789012:role/workload"
	if _, err := NewCreatePlan(context.Background(), naming.Default, cfg, "123456789012", "us-east-1", DefaultHelixVersion, nil); err == nil || !strings.Contains(err.Error(), "instanceProfile") {
		t.Errorf("role ARN error = %v, want an instanceProfile error", err)
	}
}
