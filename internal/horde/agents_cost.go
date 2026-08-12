package horde

import (
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

// AgentsCostResources returns the cost inputs for the Horde agents module.
// Cost is estimated as desiredCapacity × instance type hourly rate × 730 hours.
// When scaling is enabled, two CloudWatch alarms are added to the cost model.
func AgentsCostResources(cfg config.HordeAgentsConfig) []cost.Resource {
	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = "c7i.xlarge"
	}

	desired := cfg.DesiredCapacity
	if desired <= 0 {
		desired = 1
	}

	resources := []cost.Resource{
		{TypeName: cloud.TypeAWSAutoScalingAutoScalingGroup, Name: fmt.Sprintf("%s x%d", instanceType, desired)},
	}

	// When queue scaling is enabled, add the two CloudWatch alarms to the cost model.
	if cfg.Scaling.Enabled {
		resources = append(resources, cost.Resource{TypeName: cloud.TypeAWSCloudWatchAlarm, Name: "Scale-out alarm"})
		resources = append(resources, cost.Resource{TypeName: cloud.TypeAWSCloudWatchAlarm, Name: "Scale-in alarm"})
	}

	return resources
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
	cost.Global.Register(cloud.TypeAWSCloudWatchAlarm, cloudWatchAlarmEstimator{})
}

// cloudWatchAlarmEstimator provides cost estimates for CloudWatch alarms.
// Standard CloudWatch alarms are ~$0.02/month each (free tier covers 10 alarms).
type cloudWatchAlarmEstimator struct{}

func (cloudWatchAlarmEstimator) Estimate(r cost.Resource) (cost.Monthly, error) {
	return cost.Monthly{
		Amount:     0.02,
		Confidence: cost.High,
		Note:       fmt.Sprintf("%s (CloudWatch alarm, free tier covers 10)", r.Name),
	}, nil
}
