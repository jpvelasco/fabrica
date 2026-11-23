package lore

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

// lore.instanceProfile is resolved when the plan is built; a malformed value fails there.
func TestNewCreatePlanRejectsMalformedProfile(t *testing.T) {
	cfg := config.Defaults().Lore
	cfg.AmiID = "ami-test123"
	cfg.VPCId = "vpc-test"
	cfg.SubnetId = "subnet-test"
	cfg.InstanceProfile = "arn:aws:iam::123456789012:role/workload"
	_, err := NewCreatePlan(context.Background(), naming.Default, cfg, "123456789012", "us-east-1", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "instanceProfile") {
		t.Errorf("NewCreatePlan(role ARN) error = %v, want an instanceProfile error", err)
	}
}
