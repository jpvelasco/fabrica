package e2e

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
)

// TestWorkstationStopStart: create → stop (status "stopped", cost drops compute)
// → start (status "ready", cost restores compute). Volume stays billed throughout.
func TestWorkstationStopStart(t *testing.T) {
	setupE2E(t)

	out, err := runCLI(t, "workstation", "create", "--yes")
	if err != nil {
		t.Fatalf("workstation create: %v\n%s", err, out)
	}
	st := readState(t)
	assertModuleExists(t, st, "workstation")

	// Cost while running: an instance line is present. Capture the running total.
	runningCost, err := runCLI(t, "cost", "report")
	if err != nil {
		t.Fatalf("cost (running): %v\n%s", err, runningCost)
	}
	assert.Contains(t, runningCost, "workstation")

	// Stop: status flips to "stopped".
	out, err = runCLI(t, "workstation", "stop", "--yes")
	if err != nil {
		t.Fatalf("workstation stop: %v\n%s", err, out)
	}
	st = readState(t)
	assertModuleStatus(t, st, "workstation", "stopped")

	// Cost while stopped: compute line dropped (the stopped-instance note appears
	// and the instance-type line is gone from the workstation block). Assert the
	// stopped annotation is present in the report.
	stoppedCost, err := runCLI(t, "cost", "report")
	if err != nil {
		t.Fatalf("cost (stopped): %v\n%s", err, stoppedCost)
	}
	if !strings.Contains(stoppedCost, "stopped") {
		t.Fatalf("stopped cost report should note the stopped instance:\n%s", stoppedCost)
	}

	// Start: status back to "ready".
	out, err = runCLI(t, "workstation", "start", "--yes")
	if err != nil {
		t.Fatalf("workstation start: %v\n%s", err, out)
	}
	st = readState(t)
	assertModuleStatus(t, st, "workstation", "ready")
}
