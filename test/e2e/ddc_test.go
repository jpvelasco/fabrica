package e2e

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
)

// TestDDCLifecycle: setup → status → region add → status (edge) → cost →
// destroy for the ddc module.
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

	// Additional edge region: dry-run first, then real.
	out, err = runCLI(t, "ddc", "region", "add", "eu-west-1", "--dry-run")
	if err != nil {
		t.Fatalf("region add --dry-run: %v\n%s", err, out)
	}
	assert.Contains(t, out, "dry run")
	assert.Contains(t, out, "eu-west-1")

	out, err = runCLI(t, "ddc", "region", "add", "eu-west-1", "--yes")
	if err != nil {
		t.Fatalf("region add: %v\n%s", err, out)
	}
	assert.Contains(t, out, "provisioned")
	st = readState(t)
	m := st.GetModule("ddc")
	if m == nil {
		t.Fatal("ddc module missing after region add")
	}
	edgeCount := 0
	for _, r := range m.Resources {
		if r.Properties != nil && r.Properties["role"] == "edge" && r.Properties["region"] == "eu-west-1" {
			edgeCount++
		}
	}
	if edgeCount != 2 {
		t.Fatalf("edge resources = %d, want 2 (SG + instance); module: %+v", edgeCount, m.Resources)
	}

	// Idempotent re-run.
	out, err = runCLI(t, "ddc", "region", "add", "eu-west-1", "--yes")
	if err != nil {
		t.Fatalf("region add re-run: %v\n%s", err, out)
	}
	assert.Contains(t, out, "already provisioned")

	// Status must list the edge region.
	out, err = runCLI(t, "ddc", "status")
	if err != nil {
		t.Fatalf("status (edges): %v\n%s", err, out)
	}
	assert.Contains(t, out, "eu-west-1")
	assert.Contains(t, out, "Edge regions")

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
