package workstation

import (
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/ec2cost"
	"github.com/jpvelasco/fabrica/internal/schedule"
)

// CostResources returns the cost inputs for a workstation at the given config.
// The cost path uses config + defaults; template overrides only apply at create time.
func CostResources(cfg config.WorkstationConfig) []cost.Resource {
	instanceType, volumeSize := resolveSizing(cfg, "")
	factor, ferr := schedule.CostFactor(cfg.Spot, cfg.Schedule)
	res := applyCapacity(CostResourcesFor(instanceType, volumeSize), factor)
	if ferr != nil {
		for i, r := range res {
			if r.TypeName == cloud.TypeAWSEC2Instance {
				res[i].Name += " (schedule invalid — estimate not discounted)"
			}
		}
	}
	return res
}

func applyCapacity(res []cost.Resource, factor float64) []cost.Resource {
	if factor >= 0.999 {
		return res
	}
	out := make([]cost.Resource, len(res))
	copy(out, res)
	for i, r := range out {
		if r.TypeName == cloud.TypeAWSEC2Instance {
			out[i].Name = schedule.EncodeFactor(r.Name, factor)
		}
	}
	return out
}

// CostResourcesFor builds cost resources from explicit resolved sizing so
// create-time estimates price the shape that will actually be provisioned,
// including template-derived shapes.
func CostResourcesFor(instanceType string, volumeSize int) []cost.Resource {
	return ec2cost.ResourcesWithDefaults(instanceType, DefaultInstanceType, volumeSize, DefaultVolumeSize)
}
