package horde

import "context"

// VPCResolver resolves VPC and subnet IDs without requiring AWS SDK imports here.
type VPCResolver interface {
	ResolveDefaultVPC(ctx context.Context) (vpcID, subnetID string, err error)
}
