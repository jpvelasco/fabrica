package workstation

import "testing"

func TestDefaultAllowedCIDRIsPrivate(t *testing.T) {
	// DefaultAllowedCIDR must be a private range, not 0.0.0.0/0.
	// This prevents workstation create from opening port 8443 to the internet by default.
	if DefaultAllowedCIDR == "0.0.0.0/0" {
		t.Error("DefaultAllowedCIDR must not be 0.0.0.0/0 — use a private CIDR like 10.0.0.0/8")
	}
}

func TestDefaultConstants(t *testing.T) {
	if DefaultInstanceType != "g4dn.xlarge" {
		t.Errorf("DefaultInstanceType = %q, want g4dn.xlarge", DefaultInstanceType)
	}
	if DefaultVolumeSize != 100 {
		t.Errorf("DefaultVolumeSize = %d, want 100", DefaultVolumeSize)
	}
	if DefaultDCVPort != 8443 {
		t.Errorf("DefaultDCVPort = %d, want 8443", DefaultDCVPort)
	}
	if DefaultIdleTimeoutMinutes != 60 {
		t.Errorf("DefaultIdleTimeoutMinutes = %d, want 60", DefaultIdleTimeoutMinutes)
	}
}
