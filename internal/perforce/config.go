package perforce

import "context"

const DefaultHelixVersion = "2024.2"

// VPCResolver resolves VPC and subnet IDs. The AWS provider implements this
// via ec2:DescribeVpcs so that internal/perforce stays free of AWS SDK imports.
type VPCResolver interface {
	ResolveDefaultVPC(ctx context.Context) (vpcID, subnetID string, err error)
}
