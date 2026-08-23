package provision

import (
	"context"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

// VPCResolver returns the provider's default-VPC resolver, or nil when the
// provider does not implement cloud.VPCResolver. Create/setup commands pass
// the result straight into their plan constructors so plans resolve the
// account default VPC when config omits vpcId/subnetId.
func VPCResolver(p cloud.Provider) cloud.VPCResolver {
	if vr, ok := p.(cloud.VPCResolver); ok {
		return vr
	}
	return nil
}

// VolumeTagger returns the provider's EBS volume tagger, or nil when it does
// not implement cloud.VolumeTagger. Instance create steps use it in PostCreate
// to tag BlockDeviceMapping volumes, which Cloud Control cannot tag at creation.
func VolumeTagger(p cloud.Provider) cloud.VolumeTagger {
	if vt, ok := p.(cloud.VolumeTagger); ok {
		return vt
	}
	return nil
}

// TagVolumesPostCreate returns a CreateStep.PostCreate hook that tags every
// EBS volume attached to the freshly created instance (ManagedBy,
// FabricaModule, Name). No-op when the provider lacks cloud.VolumeTagger.
func TagVolumesPostCreate(p cloud.Provider, module, instanceName string) func(context.Context, string) error {
	tagger := VolumeTagger(p)
	return func(ctx context.Context, instanceID string) error {
		if tagger == nil {
			return nil
		}
		return tagger.TagInstanceVolumes(ctx, instanceID, map[string]string{
			"ManagedBy":     "fabrica",
			"FabricaModule": module,
			"Name":          instanceName,
		})
	}
}
