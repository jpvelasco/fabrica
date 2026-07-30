// Package ec2cost provides shared cost-resource builders for EC2-based modules.
// All modules that provision an EC2 instance (horde, lore, workstation) use this
// instead of duplicating the EC2 instance + volume resource construction.
package ec2cost

import (
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/cost"
)

// InstanceAndVolume returns the standard EC2 instance + EBS volume cost resources
// for any EC2-based module. Callers resolve their module-specific defaults before
// calling this helper.
func InstanceAndVolume(instanceType string, volumeSize int) []cost.Resource {
	return []cost.Resource{
		{TypeName: cloud.TypeAWSEC2Instance, Name: instanceType},
		{TypeName: cloud.TypeAWSEC2Volume, Name: fmt.Sprintf("gp3-%dGiB", volumeSize)},
	}
}

// ResourcesWithDefaults builds cost resources for an EC2 instance + EBS volume,
// applying default values when the config values are zero. This eliminates the
// repeated default-resolution boilerplate across module cost.go files.
func ResourcesWithDefaults(instanceType string, defaultType string, volumeSize int, defaultSize int) []cost.Resource {
	if instanceType == "" {
		instanceType = defaultType
	}
	if volumeSize <= 0 {
		volumeSize = defaultSize
	}
	return InstanceAndVolume(instanceType, volumeSize)
}
