package horde

import (
	"fmt"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

// CostResources returns the cost inputs for a Horde module at the given config,
// applying the same defaults as NewCreatePlan.
func CostResources(cfg config.HordeConfig) []cost.Resource {
	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = "m7i.2xlarge"
	}
	volumeSize := cfg.VolumeSize
	if volumeSize <= 0 {
		volumeSize = 100
	}
	return []cost.Resource{
		{TypeName: TypeAWSEC2Instance, Name: instanceType},
		{TypeName: TypeAWSEC2Volume, Name: fmt.Sprintf("gp3-%dGiB", volumeSize)},
	}
}
