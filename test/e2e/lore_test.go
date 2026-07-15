package e2e

import "testing"

// TestLoreLifecycle: create writes state → status sees it → cost prices it →
// destroy removes it.
func TestLoreLifecycle(t *testing.T) {
	assertEC2ModuleLifecycle(t, "lore", "Lore server provisioned.")
}
