package horde

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	_ "github.com/jpvelasco/fabrica/internal/perforce" // registers EC2 instance estimator
)

func TestAgentsCostResources_Defaults(t *testing.T) {
	resources := AgentsCostResources(config.HordeAgentsConfig{})
	if len(resources) != 1 {
		t.Fatalf("want 1 resource, got %d", len(resources))
	}
	r := resources[0]
	if r.TypeName != cloud.TypeAWSAutoScalingAutoScalingGroup {
		t.Errorf("TypeName = %q, want ASG", r.TypeName)
	}
	if r.Name != "c7i.xlarge x1" {
		t.Errorf("Name = %q, want c7i.xlarge x1", r.Name)
	}
}

func TestAgentsCostResources_CustomInstanceType(t *testing.T) {
	resources := AgentsCostResources(config.HordeAgentsConfig{
		InstanceType: "c7i.2xlarge",
	})
	if resources[0].Name != "c7i.2xlarge x1" {
		t.Errorf("Name = %q, want c7i.2xlarge x1", resources[0].Name)
	}
}

func TestAgentsCostResources_CustomDesiredCapacity(t *testing.T) {
	resources := AgentsCostResources(config.HordeAgentsConfig{
		DesiredCapacity: 3,
	})
	if resources[0].Name != "c7i.xlarge x3" {
		t.Errorf("Name = %q, want c7i.xlarge x3", resources[0].Name)
	}
}

func TestAgentsCostResources_BothCustom(t *testing.T) {
	resources := AgentsCostResources(config.HordeAgentsConfig{
		InstanceType:    "c7i.xlarge",
		DesiredCapacity: 2,
	})
	if resources[0].Name != "c7i.xlarge x2" {
		t.Errorf("Name = %q, want c7i.xlarge x2", resources[0].Name)
	}
}

func TestAgentsCostResources_ZeroDesiredDefaultsToOne(t *testing.T) {
	resources := AgentsCostResources(config.HordeAgentsConfig{
		DesiredCapacity: 0,
	})
	if resources[0].Name != "c7i.xlarge x1" {
		t.Errorf("Name = %q, want c7i.xlarge x1 (zero defaults to 1)", resources[0].Name)
	}
}

func TestAgentsCostResources_NegativeDesiredDefaultsToOne(t *testing.T) {
	resources := AgentsCostResources(config.HordeAgentsConfig{
		DesiredCapacity: -1,
	})
	if resources[0].Name != "c7i.xlarge x1" {
		t.Errorf("Name = %q, want c7i.xlarge x1 (negative defaults to 1)", resources[0].Name)
	}
}

// TestASGEstimator_Success tests the ASG estimator via the global registry.
// The asgEstimator delegates to the EC2 instance estimator (registered in
// internal/perforce/cost.go) so we can test through cost.Global.Estimate.
func TestASGEstimator_Success(t *testing.T) {
	monthly, err := cost.Global.Estimate(cloud.TypeAWSAutoScalingAutoScalingGroup, cost.Resource{
		TypeName: cloud.TypeAWSAutoScalingAutoScalingGroup,
		Name:     "c7i.xlarge x2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if monthly.Amount <= 0 {
		t.Errorf("Amount = %.2f, want positive", monthly.Amount)
	}
	if monthly.Confidence != cost.High {
		t.Errorf("Confidence = %v, want High", monthly.Confidence)
	}
	if monthly.Note == "" {
		t.Error("Note should not be empty")
	}
}

// TestASGEstimator_ParseError tests the asgEstimator directly with a bad name.
func TestASGEstimator_ParseError(t *testing.T) {
	est := asgEstimator{}
	_, err := est.Estimate(cost.Resource{
		TypeName: cloud.TypeAWSAutoScalingAutoScalingGroup,
		Name:     "bad format",
	})
	if err == nil {
		t.Fatal("expected error for bad format")
	}
}

// TestASGEstimator_ZeroCount tests the asgEstimator directly with zero count.
func TestASGEstimator_ZeroCount(t *testing.T) {
	est := asgEstimator{}
	_, err := est.Estimate(cost.Resource{
		TypeName: cloud.TypeAWSAutoScalingAutoScalingGroup,
		Name:     "c7i.xlarge x0",
	})
	if err == nil {
		t.Fatal("expected error for zero count")
	}
}

// TestASGEstimator_SingleInstance tests the ASG estimator via the global registry
// with a single instance (c7i.xlarge x1).
func TestASGEstimator_SingleInstance(t *testing.T) {
	monthly, err := cost.Global.Estimate(cloud.TypeAWSAutoScalingAutoScalingGroup, cost.Resource{
		TypeName: cloud.TypeAWSAutoScalingAutoScalingGroup,
		Name:     "c7i.xlarge x1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Single instance should have a positive cost.
	if monthly.Amount <= 0 {
		t.Errorf("Amount = %.2f, want positive", monthly.Amount)
	}
}

// TestASGEstimator_MultiInstance verifies that N instances produce N× the unit cost.
func TestASGEstimator_MultiInstance(t *testing.T) {
	// Get cost for 1 instance.
	one, err := cost.Global.Estimate(cloud.TypeAWSAutoScalingAutoScalingGroup, cost.Resource{
		TypeName: cloud.TypeAWSAutoScalingAutoScalingGroup,
		Name:     "c7i.xlarge x1",
	})
	if err != nil {
		t.Fatalf("1x estimate: %v", err)
	}

	// Get cost for 2 instances.
	two, err := cost.Global.Estimate(cloud.TypeAWSAutoScalingAutoScalingGroup, cost.Resource{
		TypeName: cloud.TypeAWSAutoScalingAutoScalingGroup,
		Name:     "c7i.xlarge x2",
	})
	if err != nil {
		t.Fatalf("2x estimate: %v", err)
	}

	if two.Amount != one.Amount*2 {
		t.Errorf("2x amount = %.2f, want %.2f (2 x %.2f)", two.Amount, one.Amount*2, one.Amount)
	}
}

// TestASGEstimator_UnknownInstanceType tests the error path when the
// delegated EC2 estimator has no price data for the instance type.
func TestASGEstimator_UnknownInstanceType(t *testing.T) {
	est := asgEstimator{}
	_, err := est.Estimate(cost.Resource{
		TypeName: cloud.TypeAWSAutoScalingAutoScalingGroup,
		Name:     "madeup.instance x1",
	})
	if err == nil {
		t.Fatal("expected error for unknown instance type")
	}
}
