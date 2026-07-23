package ddc

import (
	"fmt"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
)

// CostResources returns cost inputs for the DDC module at the given config,
// applying the same defaults as NewSetupPlan.
// Reuses AWS::EC2::Instance / Volume / S3 estimators already registered;
// does not re-register TypeNames.
func CostResources(cfg config.DDCConfig) []cost.Resource {
	instanceType := cfg.InstanceType
	if instanceType == "" {
		instanceType = DefaultInstanceType
	}
	volumeSize := cfg.VolumeSize
	if volumeSize <= 0 {
		volumeSize = DefaultVolumeSize
	}
	bucket := cfg.Bucket
	if bucket == "" {
		bucket = "fabrica-ddc"
	}
	backend := cfg.Backend
	if backend == "" {
		backend = BackendZen
	}

	out := []cost.Resource{
		{TypeName: cloud.TypeAWSS3Bucket, Name: bucket},
		{TypeName: cloud.TypeAWSEC2Instance, Name: instanceType},
		{TypeName: cloud.TypeAWSEC2Volume, Name: fmt.Sprintf("gp3-%dGiB", volumeSize)},
	}
	if backend == BackendScylla {
		scyllaType := cfg.ScyllaInstanceType
		if scyllaType == "" {
			scyllaType = DefaultScyllaInstanceType
		}
		scyllaVol := cfg.ScyllaVolumeSize
		if scyllaVol <= 0 {
			scyllaVol = DefaultScyllaVolumeSize
		}
		out = append(out,
			cost.Resource{TypeName: cloud.TypeAWSEC2Instance, Name: scyllaType},
			cost.Resource{TypeName: cloud.TypeAWSEC2Volume, Name: fmt.Sprintf("gp3-%dGiB", scyllaVol)},
		)
	}
	return out
}
