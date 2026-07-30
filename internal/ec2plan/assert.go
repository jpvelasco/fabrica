// Package ec2plan provides shared base struct and constructor for EC2-based
// module plans, plus assertion helpers for plan-layer tests.
package ec2plan

import "testing"

// AssertBase checks the common Base fields of a CreatePlan against expected
// values. Only non-zero/empty fields in want are checked, so callers can
// assert a subset without repeating Account, Region, SGName, etc.
func AssertBase(t *testing.T, got, want Base) {
	t.Helper()
	checkStr(t, got.Account, want.Account, "Account")
	checkStr(t, got.Region, want.Region, "Region")
	checkStr(t, got.InstanceType, want.InstanceType, "InstanceType")
	checkInt(t, got.VolumeSize, want.VolumeSize, "VolumeSize")
	checkStr(t, got.AllowedCIDR, want.AllowedCIDR, "AllowedCIDR")
	checkStr(t, got.SGName, want.SGName, "SGName")
	checkStr(t, got.InstanceName, want.InstanceName, "InstanceName")
}

func checkStr(t *testing.T, got, want, name string) {
	t.Helper()
	if want != "" && got != want {
		t.Errorf("%s = %q, want %q", name, got, want)
	}
}

func checkInt(t *testing.T, got, want int, name string) {
	t.Helper()
	if want > 0 && got != want {
		t.Errorf("%s = %d, want %d", name, got, want)
	}
}

// AssertVPC checks VPC ID and subnet ID against expected values.
func AssertVPC(t *testing.T, got Base, wantVPC, wantSubnet string) {
	t.Helper()
	if got.VPCID != wantVPC {
		t.Errorf("VPCID = %q, want %q", got.VPCID, wantVPC)
	}
	if got.SubnetID != wantSubnet {
		t.Errorf("SubnetID = %q, want %q", got.SubnetID, wantSubnet)
	}
}

// AssertVPCResolved checks that VPC resolution via a resolver produced the
// expected VPC ID, subnet ID, and DefaultVPC=true.
func AssertVPCResolved(t *testing.T, got Base, wantVPC, wantSubnet string) {
	t.Helper()
	AssertVPC(t, got, wantVPC, wantSubnet)
	if !got.DefaultVPC {
		t.Error("DefaultVPC = false, want true (resolved via resolver)")
	}
}

// AssertVPCExplicit checks that explicit VPC IDs were preserved and
// DefaultVPC is false.
func AssertVPCExplicit(t *testing.T, got Base, wantVPC, wantSubnet string) {
	t.Helper()
	AssertVPC(t, got, wantVPC, wantSubnet)
	if got.DefaultVPC {
		t.Error("DefaultVPC = true, want false (explicit VPC provided)")
	}
}
