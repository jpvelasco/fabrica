package perforce

import (
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cost"
)

const (
	TypeAWSEC2Instance = "AWS::EC2::Instance"
	TypeAWSEC2Volume   = "AWS::EC2::Volume"

	// gp3 EBS pricing: $0.08/GiB-month (us-east-1).
	gp3PricePerGiB = 0.08
)

// ec2InstancePrices is an on-demand price table for common instance types
// (us-east-1, Linux, on-demand). Source: AWS pricing as of 2024-Q4.
var ec2InstancePrices = map[string]float64{
	"m5.large":     0.096,
	"m5.xlarge":    0.192,
	"m5.2xlarge":   0.384,
	"m5.4xlarge":   0.768,
	"m5.8xlarge":   1.536,
	"c5.large":     0.085,
	"c5.xlarge":    0.170,
	"c5.2xlarge":   0.340,
	"c5.4xlarge":   0.680,
	"r5.large":     0.126,
	"r5.xlarge":    0.252,
	"r5.2xlarge":   0.504,
	"r5.4xlarge":   1.008,
	"m7i.large":    0.1008,
	"m7i.xlarge":   0.2016,
	"m7i.2xlarge":  0.4032,
	"m7i.4xlarge":  0.8064,
	"m7i.8xlarge":  1.6128,
	"m7i.12xlarge": 2.4192,
	"m7i.16xlarge": 3.2256,
}

const hoursPerMonth = 730.0

type ec2InstanceEstimator struct{}

func (ec2InstanceEstimator) Estimate(r cost.Resource) (cost.Monthly, error) {
	hourly, ok := ec2InstancePrices[r.Name]
	if !ok {
		return cost.Monthly{}, fmt.Errorf("no price data for EC2 instance type %q", r.Name)
	}
	return cost.Monthly{
		Amount:     hourly * hoursPerMonth,
		Confidence: cost.High,
	}, nil
}

type ec2VolumeEstimator struct{}

func (ec2VolumeEstimator) Estimate(r cost.Resource) (cost.Monthly, error) {
	// Name is e.g. "gp3-500GiB"; we use the plan's VolumeSize directly.
	// The estimator receives a pre-built Resource from CreatePlan.CostResources;
	// we extract GiB from the Name field using a simple parse.
	var gib int
	_, err := fmt.Sscanf(r.Name, "gp3-%dGiB", &gib)
	if err != nil || gib <= 0 {
		return cost.Monthly{}, fmt.Errorf("cannot parse volume size from resource name %q", r.Name)
	}
	return cost.Monthly{
		Amount:     float64(gib) * gp3PricePerGiB,
		Confidence: cost.High,
	}, nil
}

func init() {
	cost.Global.Register(TypeAWSEC2Instance, ec2InstanceEstimator{})
	cost.Global.Register(TypeAWSEC2Volume, ec2VolumeEstimator{})
}
