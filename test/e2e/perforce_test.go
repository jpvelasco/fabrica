package e2e

import "testing"

// TestPerforceLifecycle: create writes state → status sees it → cost prices it →
// destroy removes it. The cross-command chain is the point.
func TestPerforceLifecycle(t *testing.T) {
	setupE2E(t)

	// Provision.
	out, err := runCLI(t, "perforce", "create", "--yes")
	if err != nil {
		t.Fatalf("perforce create: %v\n%s", err, out)
	}
	assertContains(t, out, "Perforce Helix Core provisioned.")

	// State has the module + its EC2 instance and security group.
	st := readState(t)
	assertModuleExists(t, st, "perforce")
	assertResourceType(t, st, "perforce", "AWS::EC2::Instance")
	assertResourceType(t, st, "perforce", "AWS::EC2::SecurityGroup")

	// status sees the provisioned module (prints the module name).
	out, err = runCLI(t, "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	assertContains(t, out, "perforce")

	// cost report prices the module (perforce line + a positive Total).
	out, err = runCLI(t, "cost", "report")
	if err != nil {
		t.Fatalf("cost report: %v\n%s", err, out)
	}
	assertContains(t, out, "perforce")
	assertContains(t, out, "Total:")

	// Tear it down.
	out, err = runCLI(t, "perforce", "destroy", "--yes")
	if err != nil {
		t.Fatalf("perforce destroy: %v\n%s", err, out)
	}

	// Module is gone from state.
	st = readState(t)
	assertModuleAbsent(t, st, "perforce")
}
