package horde

import (
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

// AgentsCostResources returns the cost inputs for the Horde agents module.
// Cost is estimated as desiredCapacity × instance type hourly rate × 730 hours.
func AgentsCostResources(cfg config.HordeAgentsConfig) []cost.Resource {
	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = "c7i.xlarge"
	}

	desired := cfg.DesiredCapacity
	if desired <= 0 {
		desired = 1
	}

	// Report as ASG resource type so the ASG estimator handles the ×N pricing.
	return []cost.Resource{
		{TypeName: cloud.TypeAWSAutoScalingAutoScalingGroup, Name: fmt.Sprintf("%s x%d", instanceType, desired)},
	}
}

// asgEstimator provides cost estimates for ASG-managed EC2 instances.
// The Name field encodes "instanceType xN" (e.g. "c7i.xlarge x2").
// It delegates to the EC2 instance estimator for per-unit pricing.
type asgEstimator struct{}

func (asgEstimator) Estimate(r cost.Resource) (cost.Monthly, error) {
	// Parse "c7i.xlarge x2" from the Name field.
	var instanceType string
	var count int
	_, err := fmt.Sscanf(r.Name, "%s x%d", &instanceType, &count)
	if err != nil || count <= 0 {
		return cost.Monthly{}, fmt.Errorf("cannot parse ASG instance spec from %q (expected 'type xN')", r.Name)
	}

	// Delegate to the EC2 instance estimator for per-unit pricing.
	unitRes := cost.Resource{TypeName: cloud.TypeAWSEC2Instance, Name: instanceType}
	unitMonthly, err := cost.Global.Estimate(cloud.TypeAWSEC2Instance, unitRes)
	if err != nil {
		return cost.Monthly{}, fmt.Errorf("estimating %s: %w", instanceType, err)
	}

	return cost.Monthly{
		Amount:     unitMonthly.Amount * float64(count),
		Confidence: cost.High,
		Note:       fmt.Sprintf("%d x %s instances (ASG desired capacity)", count, instanceType),
	}, nil
}

func init() {
	cost.Global.Register(cloud.TypeAWSAutoScalingAutoScalingGroup, asgEstimator{})
}
