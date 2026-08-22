package workstation

import (
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/ec2cost"
)

// CostResources returns the cost inputs for a workstation at the given config.
// The cost path uses config + defaults; template overrides only apply at create time.
func CostResources(cfg config.WorkstationConfig) []cost.Resource {
	instanceType, volumeSize := resolveSizing(cfg, "")
	return CostResourcesFor(instanceType, volumeSize)
}

// CostResourcesFor builds cost resources from explicit resolved sizing so
// create-time estimates price the shape that will actually be provisioned,
// including template-derived shapes.
func CostResourcesFor(instanceType string, volumeSize int) []cost.Resource {
	return ec2cost.ResourcesWithDefaults(instanceType, DefaultInstanceType, volumeSize, DefaultVolumeSize)
}
