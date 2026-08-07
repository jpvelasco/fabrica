package cloud

import "context"

// VPCResolver resolves the default VPC and subnet IDs for a provider. Module
// plan layers (perforce, horde, workstation) accept this interface so they can
// resolve networking without importing a provider's SDK; the AWS provider
// implements it via ec2:DescribeVpcs.
type VPCResolver interface {
	ResolveDefaultVPC(ctx context.Context) (vpcID, subnetID string, err error)
}

// VPCCIDRResolver resolves the CIDR block for a given VPC ID. Used by the horde
// module to default allowedCidr to the actual VPC CIDR instead of a hard-coded
// value. The AWS provider implements it via ec2:DescribeVpcs.
type VPCCIDRResolver interface {
	ResolveVPCCIDR(ctx context.Context, vpcID string) (cidr string, err error)
}

// TestVPCResolver is a shared fake VPC resolver for plan-layer tests.
// It implements the VPCResolver interface used by perforce, horde, lore,
// and workstation plan tests. The Calls field tracks how many times
// ResolveDefaultVPC was invoked (replaces ad-hoc callTracker fields).
type TestVPCResolver struct {
	VPCID    string
	SubnetID string
	Err      error
	Calls    int
}

func (f *TestVPCResolver) ResolveDefaultVPC(_ context.Context) (string, string, error) {
	f.Calls++
	return f.VPCID, f.SubnetID, f.Err
}
