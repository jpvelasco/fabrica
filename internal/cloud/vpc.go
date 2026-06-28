package cloud

import "context"

// VPCResolver resolves the default VPC and subnet IDs for a provider. Module
// plan layers (perforce, horde, workstation) accept this interface so they can
// resolve networking without importing a provider's SDK; the AWS provider
// implements it via ec2:DescribeVpcs.
type VPCResolver interface {
	ResolveDefaultVPC(ctx context.Context) (vpcID, subnetID string, err error)
}
