package deploy

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

const gameLiftHoursPerMonth = 730.0

// gameLiftInstancePrices is a small on-demand price table for GameLift-hosted
// EC2 instances (us-east-1, Linux, on-demand). GameLift EC2 pricing tracks EC2
// on-demand closely for these families. Low/Medium confidence by nature.
var gameLiftInstancePrices = map[string]float64{
	"c5.large":   0.085,
	"c5.xlarge":  0.170,
	"c5.2xlarge": 0.340,
	"c5.4xlarge": 0.680,
	"c4.large":   0.100,
	"c4.xlarge":  0.199,
	"m5.large":   0.096,
	"m5.xlarge":  0.192,
	"m5.2xlarge": 0.384,
	"r5.large":   0.126,
	"r5.xlarge":  0.252,
}

// fleetEstimator parses a "<instanceType>x<count>" name (see fleetCostName) and
// multiplies the hourly rate by the instance count and hours/month.
type fleetEstimator struct{}

func (fleetEstimator) Estimate(r cost.Resource) (cost.Monthly, error) {
	idx := strings.LastIndex(r.Name, "x")
	if idx <= 0 || idx == len(r.Name)-1 {
		return cost.Monthly{}, fmt.Errorf("cannot parse fleet cost name %q (want \"<type>x<count>\")", r.Name)
	}
	instanceType := r.Name[:idx]
	count, err := strconv.Atoi(r.Name[idx+1:])
	if err != nil || count <= 0 {
		return cost.Monthly{}, fmt.Errorf("cannot parse instance count from %q: %w", r.Name, err)
	}
	hourly, ok := gameLiftInstancePrices[instanceType]
	if !ok {
		return cost.Monthly{}, fmt.Errorf("no GameLift price data for instance type %q", instanceType)
	}
	return cost.Monthly{
		Amount:     hourly * gameLiftHoursPerMonth * float64(count),
		Confidence: cost.Medium,
		Note:       "GameLift EC2 on-demand ~= EC2 on-demand; excludes data transfer",
	}, nil
}

// freeEstimator covers GameLift builds and aliases (no standing charge).
type freeEstimator struct{ note string }

func (f freeEstimator) Estimate(cost.Resource) (cost.Monthly, error) {
	return cost.Monthly{Amount: 0, Confidence: cost.High, Note: f.note}, nil
}

// CostResources returns the standing monthly cost inputs for the deploy module:
// the active fleet. The IAM role and alias are ~free and excluded. The engine
// includes this only when a fleet resource actually exists in state.
func CostResources(cfg config.DeployConfig) []cost.Resource {
	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = defaultInstanceType
	}
	desired := cfg.DesiredInstances
	if desired <= 0 {
		desired = defaultDesiredInstances
	}
	return []cost.Resource{
		{TypeName: TypeGameLiftFleet, Name: fleetCostName(instanceType, desired)},
	}
}

func init() {
	cost.Global.Register(TypeGameLiftFleet, fleetEstimator{})
	cost.Global.Register(TypeGameLiftBuild, freeEstimator{note: "GameLift build storage is negligible"})
	cost.Global.Register(TypeGameLiftAlias, freeEstimator{note: "GameLift aliases are free"})
}
