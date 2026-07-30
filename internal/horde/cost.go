package horde

import (
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/ec2cost"
)

// CostResources returns the cost inputs for a Horde module at the given config,
// applying the same defaults as NewCreatePlan.
func CostResources(cfg config.HordeConfig) []cost.Resource {
	return ec2cost.ResourcesWithDefaults(cfg.InstanceType, "m7i.2xlarge", cfg.VolumeSize, 100)
}
