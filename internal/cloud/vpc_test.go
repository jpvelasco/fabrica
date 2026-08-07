package cloud

import (
	"context"
	"testing"
)

func TestTestVPCResolverHappyPath(t *testing.T) {
	r := &TestVPCResolver{VPCID: "vpc-123", SubnetID: "subnet-456"}
	vpc, subnet, err := r.ResolveDefaultVPC(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vpc != "vpc-123" {
		t.Errorf("VPCID = %q, want vpc-123", vpc)
	}
	if subnet != "subnet-456" {
		t.Errorf("SubnetID = %q, want subnet-456", subnet)
	}
	if r.Calls != 1 {
		t.Errorf("Calls = %d, want 1", r.Calls)
	}
}

func TestTestVPCResolverError(t *testing.T) {
	r := &TestVPCResolver{Err: ErrResourceNotFound}
	_, _, err := r.ResolveDefaultVPC(context.Background())
	if err != ErrResourceNotFound {
		t.Fatalf("expected ErrResourceNotFound, got: %v", err)
	}
	if r.Calls != 1 {
		t.Errorf("Calls = %d, want 1", r.Calls)
	}
}

func TestTestVPCResolverCallCount(t *testing.T) {
	r := &TestVPCResolver{VPCID: "vpc-abc"}
	for i := 0; i < 3; i++ {
		_, _, _ = r.ResolveDefaultVPC(context.Background())
	}
	if r.Calls != 3 {
		t.Errorf("Calls = %d, want 3", r.Calls)
	}
}

func TestTestVPCCIDRResolverHappyPath(t *testing.T) {
	r := &TestVPCCIDRResolver{CIDR: "172.31.0.0/16"}
	cidr, err := r.ResolveVPCCIDR(context.Background(), "vpc-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cidr != "172.31.0.0/16" {
		t.Errorf("CIDR = %q, want 172.31.0.0/16", cidr)
	}
	if r.Calls != 1 {
		t.Errorf("Calls = %d, want 1", r.Calls)
	}
}

func TestTestVPCCIDRResolverError(t *testing.T) {
	r := &TestVPCCIDRResolver{Err: ErrResourceNotFound}
	_, err := r.ResolveVPCCIDR(context.Background(), "vpc-123")
	if err != ErrResourceNotFound {
		t.Fatalf("expected ErrResourceNotFound, got: %v", err)
	}
	if r.Calls != 1 {
		t.Errorf("Calls = %d, want 1", r.Calls)
	}
}
