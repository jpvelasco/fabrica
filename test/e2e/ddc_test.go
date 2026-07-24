package e2e

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
)

// TestDDCLifecycle: setup → status → cost → destroy for the ddc module.
func TestDDCLifecycle(t *testing.T) {
	setupE2E(t)

	out, err := runCLI(t, "ddc", "setup", "--yes")
	if err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	assert.Contains(t, out, "DDC provisioned")
	st := readState(t)
	assertModuleExists(t, st, "ddc")
	assertResourceType(t, st, "ddc", "AWS::EC2::Instance")
	assertResourceType(t, st, "ddc", "AWS::S3::Bucket")

	out, err = runCLI(t, "ddc", "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	assert.Contains(t, out, "Distributed DDC")

	out, err = runCLI(t, "cost", "report")
	if err != nil {
		t.Fatalf("cost: %v\n%s", err, out)
	}
	assert.Contains(t, out, "ddc")

	out, err = runCLI(t, "ddc", "destroy", "--yes")
	if err != nil {
		t.Fatalf("destroy: %v\n%s", err, out)
	}
	st = readState(t)
	assertModuleAbsent(t, st, "ddc")
}
