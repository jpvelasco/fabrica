package workstation

import "context"

const (
	DefaultInstanceType       = "g4dn.xlarge"
	DefaultVolumeSize         = 100
	DefaultDCVPort            = 8443
	DefaultIdleTimeoutMinutes = 60
	DefaultAllowedCIDR        = "0.0.0.0/0"
)

// VPCResolver resolves VPC and subnet IDs. The AWS provider implements this
// via ec2:DescribeVpcs so that internal/workstation stays free of AWS SDK imports.
type VPCResolver interface {
	ResolveDefaultVPC(ctx context.Context) (vpcID, subnetID string, err error)
}
