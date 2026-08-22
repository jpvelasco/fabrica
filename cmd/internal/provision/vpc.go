package provision

import (
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
