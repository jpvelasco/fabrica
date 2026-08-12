package lore

import (
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/cost"
	"github.com/jpvelasco/fabrica/internal/ec2cost"
)

// CostResources returns the cost inputs for a Lore module at the given config,
// applying the same defaults as NewCreatePlan.
// Does not register estimators — reuses AWS::EC2::Instance / Volume from
// internal/perforce/cost.go and AWS::S3::Bucket from internal/cost.
func CostResources(cfg config.LoreConfig) []cost.Resource {
	resources := ec2cost.ResourcesWithDefaults(cfg.InstanceType, "m5.xlarge", cfg.VolumeSize, 500)

	storeBackend := normalizeStoreBackend(cfg.StoreBackend)
	if storeBackend == StoreBackendS3 {
		bucket := cfg.StoreBucket
		if bucket == "" {
			bucket = "fabrica-lore-store"
		}
		resources = append(resources, cost.Resource{
			TypeName: cloud.TypeAWSS3Bucket,
			Name:     bucket,
		})
	}

	return resources
}
