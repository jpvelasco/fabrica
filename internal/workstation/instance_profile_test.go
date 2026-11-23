package workstation

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

// workstation.instanceProfile is resolved when the plan is built; a malformed value fails there.
func TestNewCreatePlanRejectsMalformedProfile(t *testing.T) {
	cfg := config.Defaults().Workstation
	cfg.AmiID = "ami-test123"
	cfg.InstanceProfile = "arn:aws:iam::123456789012:role/workload"
	_, err := NewCreatePlan(context.Background(), naming.Default, cfg, "123456789012", "us-east-1", nil, "", "")
	if err == nil || !strings.Contains(err.Error(), "instanceProfile") {
		t.Errorf("NewCreatePlan(role ARN) error = %v, want an instanceProfile error", err)
	}
}
