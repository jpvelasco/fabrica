package horde

import (
	"context"
	"fmt"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestNewAgentsCreatePlanMissingAmiID(t *testing.T) {
	cfg := config.HordeAgentsConfig{}
	_, err := NewAgentsCreatePlan(context.Background(), cfg, "10.0.1.10", 5000, "sg-coord", "123456789012", "us-east-1", nil)
	if err == nil {
		t.Fatal("expected error when agent AMI is empty")
	}
	assert.Contains(t, err.Error(), "horde.agents.amiId is required")
}

func TestNewAgentsCreatePlanMissingCoordinatorIP(t *testing.T) {
	cfg := config.HordeAgentsConfig{AmiID: "ami-agent123"}
	_, err := NewAgentsCreatePlan(context.Background(), cfg, "", 5000, "sg-coord", "123456789012", "us-east-1", nil)
	if err == nil {
		t.Fatal("expected error when coordinator IP is empty")
	}
	assert.Contains(t, err.Error(), "coordinator private IP is not available")
}

func TestNewAgentsCreatePlanDefaults(t *testing.T) {
	cfg := config.HordeAgentsConfig{AmiID: "ami-agent123"}
	resolver := &testutilVPCResolver{vpcID: "vpc-test", subnetID: "subnet-test"}
	plan, err := NewAgentsCreatePlan(context.Background(), cfg, "10.0.1.10", 5000, "sg-coord123", "123456789012", "us-east-1", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if plan.InstanceType != "c7i.xlarge" {
		t.Errorf("InstanceType = %q, want c7i.xlarge", plan.InstanceType)
	}
	if plan.DesiredCapacity != 1 {
		t.Errorf("DesiredCapacity = %d, want 1", plan.DesiredCapacity)
	}
	if plan.MaxSize != 2 {
		t.Errorf("MaxSize = %d, want 2", plan.MaxSize)
	}
	if plan.MinSize != 0 {
		t.Errorf("MinSize = %d, want 0", plan.MinSize)
	}
	if plan.CoordinatorPrivateIP != "10.0.1.10" {
		t.Errorf("CoordinatorPrivateIP = %q, want 10.0.1.10", plan.CoordinatorPrivateIP)
	}
	if plan.CoordinatorPort != 5000 {
		t.Errorf("CoordinatorPort = %d, want 5000", plan.CoordinatorPort)
	}
	if plan.CoordinatorSGID != "sg-coord123" {
		t.Errorf("CoordinatorSGID = %q, want sg-coord123", plan.CoordinatorSGID)
	}
	if plan.ASGName != "fabrica-horde-agents-asg" {
		t.Errorf("ASGName = %q, want fabrica-horde-agents-asg", plan.ASGName)
	}
}

func TestNewAgentsCreatePlanCustomValues(t *testing.T) {
	cfg := config.HordeAgentsConfig{
		AmiID:           "ami-agent456",
		InstanceType:    "c7i.2xlarge",
		MinSize:         2,
		DesiredCapacity: 3,
		MaxSize:         5,
	}
	resolver := &testutilVPCResolver{vpcID: "vpc-test", subnetID: "subnet-test"}
	plan, err := NewAgentsCreatePlan(context.Background(), cfg, "10.0.2.20", 5000, "sg-coord", "123456789012", "us-east-1", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if plan.InstanceType != "c7i.2xlarge" {
		t.Errorf("InstanceType = %q, want c7i.2xlarge", plan.InstanceType)
	}
	if plan.MinSize != 2 {
		t.Errorf("MinSize = %d, want 2", plan.MinSize)
	}
	if plan.DesiredCapacity != 3 {
		t.Errorf("DesiredCapacity = %d, want 3", plan.DesiredCapacity)
	}
	if plan.MaxSize != 5 {
		t.Errorf("MaxSize = %d, want 5", plan.MaxSize)
	}
}

func TestNewAgentsCreatePlanMinExceedsDesired(t *testing.T) {
	cfg := config.HordeAgentsConfig{
		AmiID:           "ami-agent123",
		MinSize:         3,
		DesiredCapacity: 2,
		MaxSize:         5,
	}
	_, err := NewAgentsCreatePlan(context.Background(), cfg, "10.0.1.10", 5000, "sg-coord", "123456789012", "us-east-1", nil)
	if err == nil {
		t.Fatal("expected error when minSize > desiredCapacity")
	}
	assert.Contains(t, err.Error(), "minSize")
	assert.Contains(t, err.Error(), "desiredCapacity")
}

func TestNewAgentsCreatePlanDesiredExceedsMax(t *testing.T) {
	cfg := config.HordeAgentsConfig{
		AmiID:           "ami-agent123",
		MinSize:         0,
		DesiredCapacity: 5,
		MaxSize:         3,
	}
	_, err := NewAgentsCreatePlan(context.Background(), cfg, "10.0.1.10", 5000, "sg-coord", "123456789012", "us-east-1", nil)
	if err == nil {
		t.Fatal("expected error when desiredCapacity > maxSize")
	}
	assert.Contains(t, err.Error(), "desiredCapacity")
	assert.Contains(t, err.Error(), "maxSize")
}

func TestNewAgentsCreatePlanMinExceedsMax(t *testing.T) {
	cfg := config.HordeAgentsConfig{
		AmiID:           "ami-agent123",
		MinSize:         4,
		DesiredCapacity: 4,
		MaxSize:         2,
	}
	_, err := NewAgentsCreatePlan(context.Background(), cfg, "10.0.1.10", 5000, "sg-coord", "123456789012", "us-east-1", nil)
	if err == nil {
		t.Fatal("expected error when desiredCapacity > maxSize")
	}
	// desiredCapacity > maxSize is checked before minSize > maxSize
	assert.Contains(t, err.Error(), "desiredCapacity")
	assert.Contains(t, err.Error(), "maxSize")
}

func TestNewAgentsCreatePlanMinExceedsMaxDirect(t *testing.T) {
	// minSize == desiredCapacity > maxSize — desiredCapacity > maxSize fires first.
	cfg := config.HordeAgentsConfig{
		AmiID:           "ami-agent123",
		MinSize:         3,
		DesiredCapacity: 3,
		MaxSize:         2,
	}
	_, err := NewAgentsCreatePlan(context.Background(), cfg, "10.0.1.10", 5000, "sg-coord", "123456789012", "us-east-1", nil)
	if err == nil {
		t.Fatal("expected error when desiredCapacity > maxSize")
	}
	// desiredCapacity > maxSize fires first (3 > 2)
	assert.Contains(t, err.Error(), "desiredCapacity")
}

func TestNewAgentsCreatePlanNegativeMinSize(t *testing.T) {
	cfg := config.HordeAgentsConfig{
		AmiID:           "ami-agent123",
		MinSize:         -1,
		DesiredCapacity: 2,
		MaxSize:         4,
	}
	resolver := &testutilVPCResolver{vpcID: "vpc-test", subnetID: "subnet-test"}
	plan, err := NewAgentsCreatePlan(context.Background(), cfg, "10.0.1.10", 5000, "sg-coord", "123456789012", "us-east-1", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Negative minSize should be clamped to 0.
	if plan.MinSize != 0 {
		t.Errorf("MinSize = %d, want 0 (clamped from negative)", plan.MinSize)
	}
}

func TestNewAgentsCreatePlanVPCError(t *testing.T) {
	cfg := config.HordeAgentsConfig{AmiID: "ami-agent123"}
	resolver := &testutilVPCResolver{err: fmt.Errorf("vpc not found")}
	_, err := NewAgentsCreatePlan(context.Background(), cfg, "10.0.1.10", 5000, "sg-coord", "123456789012", "us-east-1", resolver)
	if err == nil {
		t.Fatal("expected error when VPC resolution fails")
	}
}

func TestNewAgentsCreatePlanDefaultPort(t *testing.T) {
	cfg := config.HordeAgentsConfig{AmiID: "ami-agent123"}
	resolver := &testutilVPCResolver{vpcID: "vpc-test", subnetID: "subnet-test"}
	plan, err := NewAgentsCreatePlan(context.Background(), cfg, "10.0.1.10", 0, "sg-coord", "123456789012", "us-east-1", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.CoordinatorPort != 5000 {
		t.Errorf("CoordinatorPort = %d, want 5000 (default)", plan.CoordinatorPort)
	}
}

func TestNewAgentsCreatePlanVPCResolution(t *testing.T) {
	cfg := config.HordeAgentsConfig{AmiID: "ami-agent123"}
	resolver := &testutilVPCResolver{vpcID: "vpc-custom", subnetID: "subnet-custom"}
	plan, err := NewAgentsCreatePlan(context.Background(), cfg, "10.0.1.10", 5000, "sg-coord", "123456789012", "us-east-1", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.VPCID != "vpc-custom" {
		t.Errorf("VPCID = %q, want vpc-custom", plan.VPCID)
	}
	if plan.SubnetID != "subnet-custom" {
		t.Errorf("SubnetID = %q, want subnet-custom", plan.SubnetID)
	}
	if !plan.DefaultVPC {
		t.Error("DefaultVPC should be true when resolved via resolver")
	}
}

func TestNewAgentsCreatePlanScalingEnabled(t *testing.T) {
	cfg := config.HordeAgentsConfig{
		AmiID: "ami-agent123",
		Scaling: config.HordeAgentsScalingConfig{
			Enabled:           true,
			ScaleOutThreshold: 10.0,
			ScaleInThreshold:  2.0,
			ScaleInCooldown:   120,
		},
	}
	resolver := &testutilVPCResolver{vpcID: "vpc-test", subnetID: "subnet-test"}
	plan, err := NewAgentsCreatePlan(context.Background(), cfg, "10.0.1.10", 5000, "sg-coord", "123456789012", "us-east-1", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !plan.ScalingEnabled {
		t.Error("ScalingEnabled should be true")
	}
	if plan.ScaleOutThreshold != 10.0 {
		t.Errorf("ScaleOutThreshold = %g, want 10.0", plan.ScaleOutThreshold)
	}
	if plan.ScaleInThreshold != 2.0 {
		t.Errorf("ScaleInThreshold = %g, want 2.0", plan.ScaleInThreshold)
	}
	if plan.ScaleInCooldown != 120 {
		t.Errorf("ScaleInCooldown = %d, want 120", plan.ScaleInCooldown)
	}
	if plan.MetricName != "ASGQueueDepth" {
		t.Errorf("MetricName = %q, want ASGQueueDepth (default)", plan.MetricName)
	}
	if plan.MetricNamespace != "Fabrica/HordeAgents" {
		t.Errorf("MetricNamespace = %q, want Fabrica/HordeAgents (default)", plan.MetricNamespace)
	}
	if plan.ScaleOutAlarmName != "fabrica-horde-agents-scale-out" {
		t.Errorf("ScaleOutAlarmName = %q", plan.ScaleOutAlarmName)
	}
	if plan.ScaleInAlarmName != "fabrica-horde-agents-scale-in" {
		t.Errorf("ScaleInAlarmName = %q", plan.ScaleInAlarmName)
	}
	if plan.ScaleOutPolicyName != "fabrica-horde-agents-scale-out-policy" {
		t.Errorf("ScaleOutPolicyName = %q", plan.ScaleOutPolicyName)
	}
	if plan.ScaleInPolicyName != "fabrica-horde-agents-scale-in-policy" {
		t.Errorf("ScaleInPolicyName = %q", plan.ScaleInPolicyName)
	}
}

func TestNewAgentsCreatePlanScalingDisabledByDefault(t *testing.T) {
	cfg := config.HordeAgentsConfig{AmiID: "ami-agent123"}
	resolver := &testutilVPCResolver{vpcID: "vpc-test", subnetID: "subnet-test"}
	plan, err := NewAgentsCreatePlan(context.Background(), cfg, "10.0.1.10", 5000, "sg-coord", "123456789012", "us-east-1", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.ScalingEnabled {
		t.Error("ScalingEnabled should be false by default")
	}
}

func TestNewAgentsCreatePlanScalingCustomMetric(t *testing.T) {
	cfg := config.HordeAgentsConfig{
		AmiID: "ami-agent123",
		Scaling: config.HordeAgentsScalingConfig{
			Enabled:           true,
			ScaleOutThreshold: 5.0,
			ScaleInThreshold:  1.0,
			MetricName:        "CustomQueueDepth",
			MetricNamespace:   "MyApp/Horde",
		},
	}
	resolver := &testutilVPCResolver{vpcID: "vpc-test", subnetID: "subnet-test"}
	plan, err := NewAgentsCreatePlan(context.Background(), cfg, "10.0.1.10", 5000, "sg-coord", "123456789012", "us-east-1", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.MetricName != "CustomQueueDepth" {
		t.Errorf("MetricName = %q, want CustomQueueDepth", plan.MetricName)
	}
	if plan.MetricNamespace != "MyApp/Horde" {
		t.Errorf("MetricNamespace = %q, want MyApp/Horde", plan.MetricNamespace)
	}
}

func TestNewAgentsCreatePlanScalingZeroThreshold(t *testing.T) {
	// Zero thresholds default to 5.0/1.0 when scaling is enabled.
	cfg := config.HordeAgentsConfig{
		AmiID: "ami-agent123",
		Scaling: config.HordeAgentsScalingConfig{
			Enabled:           true,
			ScaleOutThreshold: 0,
			ScaleInThreshold:  0,
		},
	}
	resolver := &testutilVPCResolver{vpcID: "vpc-test", subnetID: "subnet-test"}
	plan, err := NewAgentsCreatePlan(context.Background(), cfg, "10.0.1.10", 5000, "sg-coord", "123456789012", "us-east-1", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.ScaleOutThreshold != 5.0 {
		t.Errorf("ScaleOutThreshold = %g, want 5.0 (default)", plan.ScaleOutThreshold)
	}
	if plan.ScaleInThreshold != 1.0 {
		t.Errorf("ScaleInThreshold = %g, want 1.0 (default)", plan.ScaleInThreshold)
	}
}

func TestNewAgentsCreatePlanScalingNegativeThreshold(t *testing.T) {
	// Negative thresholds default to 5.0/1.0 when scaling is enabled.
	cfg := config.HordeAgentsConfig{
		AmiID: "ami-agent123",
		Scaling: config.HordeAgentsScalingConfig{
			Enabled:           true,
			ScaleOutThreshold: 5.0,
			ScaleInThreshold:  -1.0,
		},
	}
	resolver := &testutilVPCResolver{vpcID: "vpc-test", subnetID: "subnet-test"}
	plan, err := NewAgentsCreatePlan(context.Background(), cfg, "10.0.1.10", 5000, "sg-coord", "123456789012", "us-east-1", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Negative scaleInThreshold defaults to 1.0.
	if plan.ScaleInThreshold != 1.0 {
		t.Errorf("ScaleInThreshold = %g, want 1.0 (default for negative)", plan.ScaleInThreshold)
	}
	// Positive scaleOutThreshold is preserved.
	if plan.ScaleOutThreshold != 5.0 {
		t.Errorf("ScaleOutThreshold = %g, want 5.0", plan.ScaleOutThreshold)
	}
}

func TestNewAgentsCreatePlanScalingEnabledNoThresholds(t *testing.T) {
	// --scaling-enabled alone should apply defaults for thresholds.
	cfg := config.HordeAgentsConfig{
		AmiID: "ami-agent123",
		Scaling: config.HordeAgentsScalingConfig{
			Enabled: true,
		},
	}
	resolver := &testutilVPCResolver{vpcID: "vpc-test", subnetID: "subnet-test"}
	plan, err := NewAgentsCreatePlan(context.Background(), cfg, "10.0.1.10", 5000, "sg-coord", "123456789012", "us-east-1", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !plan.ScalingEnabled {
		t.Error("ScalingEnabled should be true")
	}
	if plan.ScaleOutThreshold != 5.0 {
		t.Errorf("ScaleOutThreshold = %g, want 5.0 (default)", plan.ScaleOutThreshold)
	}
	if plan.ScaleInThreshold != 1.0 {
		t.Errorf("ScaleInThreshold = %g, want 1.0 (default)", plan.ScaleInThreshold)
	}
	if plan.ScaleInCooldown != 300 {
		t.Errorf("ScaleInCooldown = %d, want 300 (default)", plan.ScaleInCooldown)
	}
	if plan.MetricName != "ASGQueueDepth" {
		t.Errorf("MetricName = %q, want ASGQueueDepth (default)", plan.MetricName)
	}
	if plan.MetricNamespace != "Fabrica/HordeAgents" {
		t.Errorf("MetricNamespace = %q, want Fabrica/HordeAgents (default)", plan.MetricNamespace)
	}
}

func TestNewAgentsCreatePlanScalingCooldownTooLow(t *testing.T) {
	cfg := config.HordeAgentsConfig{
		AmiID: "ami-agent123",
		Scaling: config.HordeAgentsScalingConfig{
			Enabled:           true,
			ScaleOutThreshold: 5.0,
			ScaleInThreshold:  1.0,
			ScaleInCooldown:   30,
		},
	}
	_, err := NewAgentsCreatePlan(context.Background(), cfg, "10.0.1.10", 5000, "sg-coord", "123456789012", "us-east-1", nil)
	if err == nil {
		t.Fatal("expected error when cooldown < 60")
	}
	assert.Contains(t, err.Error(), "scaleInCooldown")
}

// testutilVPCResolver implements cloud.VPCResolver for plan tests.
type testutilVPCResolver struct {
	vpcID    string
	subnetID string
	err      error
}

var _ cloud.VPCResolver = (*testutilVPCResolver)(nil)

func (r *testutilVPCResolver) ResolveDefaultVPC(_ context.Context) (string, string, error) {
	return r.vpcID, r.subnetID, r.err
}
