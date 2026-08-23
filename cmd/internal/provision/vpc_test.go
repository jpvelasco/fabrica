package provision

import (
	"context"
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

// TestTagVolumesPostCreate verifies the hook tags via the provider tagger and
// no-ops when the provider lacks cloud.VolumeTagger.
func TestTagVolumesPostCreate(t *testing.T) {
	resolving := &testutil.VPCResolverProvider{VPCID: "vpc-1", SubnetID: "subnet-1"}
	hook := TagVolumesPostCreate(resolving, "perforce", "fabrica-perforce")
	if err := hook(context.Background(), "i-1"); err != nil {
		t.Fatalf("hook on provider without VolumeTagger must no-op: %v", err)
	}

	if TagVolumesPostCreate(nil, "perforce", "n") == nil {
		t.Fatal("TagVolumesPostCreate(nil provider) returned nil func")
	}
}
