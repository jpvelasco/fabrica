package ddc

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

// ddc.instanceProfile is resolved by the home setup plan and by each edge plan,
// so a malformed value fails both before anything is created.
func TestPlansRejectMalformedProfile(t *testing.T) {
	cfg := config.Defaults().DDC
	cfg.AmiID = "ami-test123"
	cfg.VPCId = "vpc-test"
	cfg.SubnetId = "subnet-test"
	cfg.InstanceProfile = "arn:aws:iam::123456789012:role/workload"

	if _, err := NewSetupPlan(context.Background(), naming.Default, cfg, "123456789012", "us-east-1", nil); err == nil || !strings.Contains(err.Error(), "instanceProfile") {
		t.Errorf("NewSetupPlan(role ARN) error = %v, want an instanceProfile error", err)
	}
	if _, err := NewEdgePlan(context.Background(), naming.Default, cfg, "123456789012", "us-east-1", "us-west-2", EdgeOptions{}, nil); err == nil || !strings.Contains(err.Error(), "instanceProfile") {
		t.Errorf("NewEdgePlan(role ARN) error = %v, want an instanceProfile error", err)
	}
}
