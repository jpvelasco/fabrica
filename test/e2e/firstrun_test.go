package e2e

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
)

// TestFirstRun: a fresh account runs setup, then status reports the backend is
// ready with no modules yet.
func TestFirstRun(t *testing.T) {
	setupE2E(t)

	// Before setup: status says nothing is provisioned.
	out, err := runCLI(t, "status")
	if err != nil {
		t.Fatalf("status (pre-setup): %v\n%s", err, out)
	}
	assert.Contains(t, out, "Nothing provisioned yet")

	// setup --yes creates the state backend (bucket + table) via the fake.
	out, err = runCLI(t, "setup", "--yes")
	if err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	assert.Contains(t, out, "Setup complete")

	// After setup: status reports the backend is ready but no modules.
	out, err = runCLI(t, "status")
	if err != nil {
		t.Fatalf("status (post-setup): %v\n%s", err, out)
	}
	assert.Contains(t, out, "State backend is ready, but no modules are provisioned yet.")
}
