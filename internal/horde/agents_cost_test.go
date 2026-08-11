package horde

import (
	_ "github.com/jpvelasco/fabrica/internal/perforce" // registers EC2 instance estimator
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

func TestAgentsCostResourcesDefaults(t *testing.T) {
	cfg := config.HordeAgentsConfig{AmiID: "ami-agent123"}
	resources := AgentsCostResources(cfg)
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	if resources[0].TypeName != cloud.TypeAWSAutoScalingAutoScalingGroup {
		t.Errorf("TypeName = %q, want %s", resources[0].TypeName, cloud.TypeAWSAutoScalingAutoScalingGroup)
	}
	// Default: c7i.xlarge x1
	if resources[0].Name != "c7i.xlarge x1" {
		t.Errorf("Name = %q, want c7i.xlarge x1", resources[0].Name)
	}
}

func TestAgentsCostResourcesCustom(t *testing.T) {
	cfg := config.HordeAgentsConfig{
		AmiID:           "ami-agent123",
		InstanceType:    "c7i.2xlarge",
		DesiredCapacity: 3,
	}
	resources := AgentsCostResources(cfg)
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	if resources[0].Name != "c7i.2xlarge x3" {
		t.Errorf("Name = %q, want c7i.2xlarge x3", resources[0].Name)
	}
}

func TestASGEstimator(t *testing.T) {
	e := asgEstimator{}
	res := cost.Resource{
		TypeName: cloud.TypeAWSAutoScalingAutoScalingGroup,
		Name:     "m5.xlarge x2",
	}
	m, err := e.Estimate(res)
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	// m5.xlarge = $0.192/hr × 730 = $140.16/unit × 2 = $280.32
	expected := 0.192 * 730.0 * 2
	if m.Amount != expected {
		t.Errorf("Amount = %.2f, want %.2f", m.Amount, expected)
	}
	if m.Confidence != cost.High {
		t.Errorf("Confidence = %s, want High", m.Confidence)
	}
	if !strings.Contains(m.Note, "m5.xlarge") {
		t.Error("Note should mention instance type")
	}
}

func TestASGEstimatorC7i(t *testing.T) {
	e := asgEstimator{}
	res := cost.Resource{
		TypeName: cloud.TypeAWSAutoScalingAutoScalingGroup,
		Name:     "c7i.xlarge x1",
	}
	m, err := e.Estimate(res)
	if err != nil {
		t.Fatalf("Estimate: %v", err)
	}
	// c7i.xlarge = $0.170/hr × 730 ≈ $124.10
	expected := 0.170 * 730.0
	if m.Amount < expected-0.01 || m.Amount > expected+0.01 {
		t.Errorf("Amount = %.4f, want ≈%.4f", m.Amount, expected)
	}
	if m.Confidence != cost.High {
		t.Errorf("Confidence = %s, want High", m.Confidence)
	}
}

func TestASGEstimatorUnknownType(t *testing.T) {
	e := asgEstimator{}
	res := cost.Resource{
		TypeName: cloud.TypeAWSAutoScalingAutoScalingGroup,
		Name:     "unknown.type x1",
	}
	_, err := e.Estimate(res)
	if err == nil {
		t.Fatal("expected error for unknown instance type")
	}
	assert.Contains(t, err.Error(), "estimating")
}

func TestASGEstimatorBadFormat(t *testing.T) {
	e := asgEstimator{}
	res := cost.Resource{
		TypeName: cloud.TypeAWSAutoScalingAutoScalingGroup,
		Name:     "bad-format",
	}
	_, err := e.Estimate(res)
	if err == nil {
		t.Fatal("expected error for bad format")
	}
	assert.Contains(t, err.Error(), "cannot parse")
}

func TestASGEstimatorRegistered(t *testing.T) {
	_, err := cost.Global.Get(cloud.TypeAWSAutoScalingAutoScalingGroup)
	if err != nil {
		t.Fatalf("ASG estimator not registered: %v", err)
	}
}
