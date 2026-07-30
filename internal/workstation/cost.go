package workstation

import (
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/ec2cost"
)

// CostResources returns the cost inputs for a workstation at the given config.
// The cost path uses config + defaults; template overrides only apply at create time.
func CostResources(cfg config.WorkstationConfig) []cost.Resource {
	return ec2cost.ResourcesWithDefaults(cfg.InstanceType, DefaultInstanceType, cfg.VolumeSize, DefaultVolumeSize)
}
