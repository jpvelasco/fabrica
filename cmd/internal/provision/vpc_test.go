package provision

import (
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
)

// TestVPCResolverFromProvider verifies the helper surfaces a provider that
// implements cloud.VPCResolver and yields nil for one that does not.
func TestVPCResolverFromProvider(t *testing.T) {
	resolving := &testutil.VPCResolverProvider{VPCID: "vpc-1", SubnetID: "subnet-1"}
	if got := VPCResolver(resolving); got == nil {
		t.Fatal("VPCResolver(VPCResolverProvider) = nil, want non-nil")
	}
	if got := VPCResolver(&testutil.TestProvider{}); got != nil {
		t.Fatalf("VPCResolver(TestProvider) = %v, want nil", got)
	}
	if got := VPCResolver(nil); got != nil {
		t.Fatalf("VPCResolver(nil) = %v, want nil", got)
	}
}
