package horde

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

func coordinatorConfig(profile string) config.HordeConfig {
	cfg := config.Defaults().Horde
	cfg.AmiID = "ami-test123"
	cfg.VPCId = "vpc-test"
	cfg.SubnetId = "subnet-test"
	cfg.InstanceProfile = profile
	return cfg
}

// The coordinator launches under horde.instanceProfile when it names one, and
// rejects a malformed value before any plan is built.
func TestNewCreatePlanCoordinatorProfile(t *testing.T) {
	plan, err := NewCreatePlan(context.Background(), naming.Default, coordinatorConfig("arn:aws:iam::123456789012:instance-profile/platform/workload"), "123456789012", "us-east-1", nil, nil)
	if err != nil {
		t.Fatalf("NewCreatePlan: %v", err)
	}
	if !plan.Profile.External() || plan.InstanceProfileName != "workload" {
		t.Errorf("plan profile = %+v, InstanceProfileName = %q; want external workload", plan.Profile, plan.InstanceProfileName)
	}

	_, err = NewCreatePlan(context.Background(), naming.Default, coordinatorConfig("arn:aws:iam::123456789012:role/workload"), "123456789012", "us-east-1", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "instanceProfile") {
		t.Errorf("NewCreatePlan(role ARN) error = %v, want an instanceProfile error", err)
	}
}
