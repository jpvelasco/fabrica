package cloud

import "context"

// TestVPCCIDRResolver is a shared fake VPCCIDRResolver for plan-layer tests.
type TestVPCCIDRResolver struct {
	CIDR  string
	Err   error
	Calls int
}

func (f *TestVPCCIDRResolver) ResolveVPCCIDR(_ context.Context, _ string) (string, error) {
	f.Calls++
	return f.CIDR, f.Err
}
