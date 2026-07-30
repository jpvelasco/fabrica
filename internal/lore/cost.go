package lore

import (
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/ec2cost"
)

// CostResources returns the cost inputs for a Lore module at the given config,
// applying the same defaults as NewCreatePlan.
// Does not register estimators — reuses AWS::EC2::Instance / Volume from
// internal/perforce/cost.go.
func CostResources(cfg config.LoreConfig) []cost.Resource {
	return ec2cost.ResourcesWithDefaults(cfg.InstanceType, "m5.xlarge", cfg.VolumeSize, 500)
}
