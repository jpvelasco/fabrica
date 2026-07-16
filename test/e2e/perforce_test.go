package e2e

import "testing"

// TestPerforceLifecycle: create writes state → status sees it → cost prices it →
// destroy removes it. The cross-command chain is the point.
func TestPerforceLifecycle(t *testing.T) {
	assertEC2ModuleLifecycle(t, "perforce", "Perforce Helix Core provisioned.")
}
