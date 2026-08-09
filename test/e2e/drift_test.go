package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/drift"
)

// TestDriftAllInSync: provision horde + CI, run drift, and assert all resources
// report inSync. This covers the happy path for EC2 instances, security groups,
// IAM roles, and CodeBuild projects.
func TestDriftAllInSync(t *testing.T) {
	setupE2E(t)

	// Setup the state backend so drift sees it.
	if out, err := runCLI(t, "setup", "--yes"); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}

	// Provision horde (EC2 instance + SG).
	if out, err := runCLI(t, "horde", "create", "--yes"); err != nil {
		t.Fatalf("horde create: %v\n%s", err, out)
	}

	// Provision CI (IAM role + CodeBuild project).
	if out, err := runCLI(t, "ci", "setup", "--yes"); err != nil {
		t.Fatalf("ci setup: %v\n%s", err, out)
	}

	// Run drift with JSON output.
	out, err := runCLI(t, "drift", "--json")
	if err != nil {
		t.Fatalf("drift: %v\n%s", err, out)
	}

	// Parse the JSON report.
	var report drift.DriftReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("drift output is not valid JSON: %v\n%s", err, out)
	}

	// Backend should be in sync (setup created it).
	if report.Backend.BucketStatus != drift.InSync {
		t.Fatalf("backend bucket status = %q, want %q", report.Backend.BucketStatus, drift.InSync)
	}
	if report.Backend.TableStatus != drift.InSync {
		t.Fatalf("backend table status = %q, want %q", report.Backend.TableStatus, drift.InSync)
	}

	// All module resources should be in sync.
	for _, md := range report.Modules {
		for _, r := range md.Resources {
			if r.Status != drift.InSync {
				t.Errorf("module %q resource %s/%s: status = %q, want %q; details: %s",
					md.Name, r.TypeName, r.Identifier, r.Status, drift.InSync, r.Details)
			}
		}
	}

	// Summary: no missing, no mismatch, no errors.
	if report.Missing > 0 {
		t.Errorf("expected 0 missing, got %d", report.Missing)
	}
	if report.Mismatch > 0 {
		t.Errorf("expected 0 mismatch, got %d", report.Mismatch)
	}
	if report.Errors > 0 {
		t.Errorf("expected 0 errors, got %d", report.Errors)
	}
}

// TestDriftMissingResource: provision horde, delete the EC2 instance from the
// fake store to simulate AWS-side deletion, and assert drift reports it as missing.
func TestDriftMissingResource(t *testing.T) {
	store := setupE2E(t)

	if out, err := runCLI(t, "setup", "--yes"); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "horde", "create", "--yes"); err != nil {
		t.Fatalf("horde create: %v\n%s", err, out)
	}

	// Find the EC2 instance identifier in the fake store and delete it.
	var instanceID string
	for id, sr := range store.resources {
		if sr.typeName == "AWS::EC2::Instance" {
			instanceID = id
			break
		}
	}
	if instanceID == "" {
		t.Fatal("no EC2 instance found in fake store")
	}
	delete(store.resources, instanceID)

	// Run drift and expect the instance to show as missing.
	out, err := runCLI(t, "drift", "--json")
	if err != nil {
		t.Fatalf("drift: %v\n%s", err, out)
	}

	var report drift.DriftReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("drift output is not valid JSON: %v\n%s", err, out)
	}

	// The SG should still be in sync; the instance should be missing.
	foundMissing := false
	for _, md := range report.Modules {
		for _, r := range md.Resources {
			if r.TypeName == "AWS::EC2::Instance" && r.Status != drift.Missing {
				t.Errorf("EC2 instance: status = %q, want %q; details: %s",
					r.Status, drift.Missing, r.Details)
			}
			if r.TypeName == "AWS::EC2::Instance" && r.Status == drift.Missing {
				foundMissing = true
			}
			if r.TypeName == "AWS::EC2::SecurityGroup" && r.Status != drift.InSync {
				t.Errorf("SG: status = %q, want %q; details: %s",
					r.Status, drift.InSync, r.Details)
			}
		}
	}
	if !foundMissing {
		t.Error("expected to find a missing EC2 instance in drift report")
	}
	if report.Missing != 1 {
		t.Errorf("expected 1 missing resource, got %d", report.Missing)
	}
}

// TestDriftMissingCodeBuildProject: provision CI, delete the project from the
// fake store, and assert drift reports the CodeBuild project as missing.
func TestDriftMissingCodeBuildProject(t *testing.T) {
	store := setupE2E(t)

	if out, err := runCLI(t, "setup", "--yes"); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "ci", "setup", "--yes"); err != nil {
		t.Fatalf("ci setup: %v\n%s", err, out)
	}

	// Delete the CodeBuild project from the fake store.
	for name := range store.projects {
		delete(store.projects, name)
		break
	}

	out, err := runCLI(t, "drift", "--json")
	if err != nil {
		t.Fatalf("drift: %v\n%s", err, out)
	}

	var report drift.DriftReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("drift output is not valid JSON: %v\n%s", err, out)
	}

	// IAM role should be in sync; CodeBuild project should be missing.
	for _, md := range report.Modules {
		for _, r := range md.Resources {
			if r.TypeName == "AWS::IAM::Role" && r.Status != drift.InSync {
				t.Errorf("IAM role: status = %q, want %q; details: %s",
					r.Status, drift.InSync, r.Details)
			}
			if r.TypeName == "AWS::CodeBuild::Project" && r.Status != drift.Missing {
				t.Errorf("CodeBuild project: status = %q, want %q; details: %s",
					r.Status, drift.Missing, r.Details)
			}
		}
	}
	if report.Missing != 1 {
		t.Errorf("expected 1 missing resource, got %d", report.Missing)
	}
}

// TestDriftNoModules: run drift on a fresh account with no modules provisioned.
func TestDriftNoModules(t *testing.T) {
	setupE2E(t)

	out, err := runCLI(t, "drift")
	if err != nil {
		t.Fatalf("drift: %v\n%s", err, out)
	}
	assert.Contains(t, out, "No modules provisioned")
}

// TestDriftBackendMissing: run drift after provisioning horde but without setup.
// The backend should show as missing since the bucket/table were never created.
func TestDriftBackendMissing(t *testing.T) {
	setupE2E(t)

	// Create horde without running setup first.
	if out, err := runCLI(t, "horde", "create", "--yes"); err != nil {
		t.Fatalf("horde create: %v\n%s", err, out)
	}

	out, err := runCLI(t, "drift", "--json")
	if err != nil {
		t.Fatalf("drift: %v\n%s", err, out)
	}

	var report drift.DriftReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("drift output is not valid JSON: %v\n%s", err, out)
	}

	// Backend bucket and table should be missing (setup was not run).
	if report.Backend.BucketStatus != drift.Missing {
		t.Fatalf("backend bucket status = %q, want %q", report.Backend.BucketStatus, drift.Missing)
	}
	if report.Backend.TableStatus != drift.Missing {
		t.Fatalf("backend table status = %q, want %q", report.Backend.TableStatus, drift.Missing)
	}
}

// TestDriftTextOutput: verify that text output contains expected status symbols.
func TestDriftTextOutput(t *testing.T) {
	store := setupE2E(t)

	if out, err := runCLI(t, "setup", "--yes"); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "horde", "create", "--yes"); err != nil {
		t.Fatalf("horde create: %v\n%s", err, out)
	}

	// Delete the instance to create a missing resource.
	for id, sr := range store.resources {
		if sr.typeName == "AWS::EC2::Instance" {
			delete(store.resources, id)
			break
		}
	}

	out, err := runCLI(t, "drift")
	if err != nil {
		t.Fatalf("drift: %v\n%s", err, out)
	}

	// Text output should contain status symbols.
	assert.Contains(t, out, "[OK]")
	assert.Contains(t, out, "[FAIL]")
	assert.Contains(t, out, "Summary")
	assert.Contains(t, out, "Missing:")
}

// TestDriftExtraResource: provision horde, then inject an extra EC2 instance
// into the fake store that is not in local state, and assert drift reports it
// as Extra.
func TestDriftExtraResource(t *testing.T) {
	store := setupE2E(t)

	if out, err := runCLI(t, "setup", "--yes"); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "horde", "create", "--yes"); err != nil {
		t.Fatalf("horde create: %v\n%s", err, out)
	}

	// Inject an extra EC2 instance into the fake store that is not in state.
	store.resources["i-extra-999"] = &storedResource{
		typeName:   "AWS::EC2::Instance",
		identifier: "i-extra-999",
		ec2Status:  "running",
	}

	out, err := runCLI(t, "drift", "--json")
	if err != nil {
		t.Fatalf("drift: %v\n%s", err, out)
	}

	var report drift.DriftReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("drift output is not valid JSON: %v\n%s", err, out)
	}

	// The recorded instance should be inSync; the extra should be Extra.
	foundExtra := false
	for _, md := range report.Modules {
		for _, r := range md.Resources {
			if r.Status == drift.Extra && r.Identifier == "i-extra-999" {
				foundExtra = true
			}
		}
	}
	if !foundExtra {
		t.Error("expected to find i-extra-999 as Extra in drift report")
	}
	if report.Extra != 1 {
		t.Errorf("expected 1 extra resource, got %d", report.Extra)
	}
}

// TestDriftFixMissingInstance: provision horde, delete the EC2 instance from
// the fake store, run drift --fix --yes to recreate it, then verify drift
// shows all inSync.
func TestDriftFixMissingInstance(t *testing.T) {
	store := setupE2E(t)

	// Setup the state backend.
	if out, err := runCLI(t, "setup", "--yes"); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}

	// Provision horde.
	if out, err := runCLI(t, "horde", "create", "--yes"); err != nil {
		t.Fatalf("horde create: %v\n%s", err, out)
	}

	// Find and delete the EC2 instance from the fake store.
	var instanceID string
	for id, sr := range store.resources {
		if sr.typeName == "AWS::EC2::Instance" {
			instanceID = id
			break
		}
	}
	if instanceID == "" {
		t.Fatal("no EC2 instance found in fake store")
	}
	delete(store.resources, instanceID)

	// Verify drift shows the instance as missing.
	out, err := runCLI(t, "drift", "--json")
	if err != nil {
		t.Fatalf("drift: %v\n%s", err, out)
	}
	var report drift.DriftReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("drift JSON parse: %v\n%s", err, out)
	}
	if report.Missing != 1 {
		t.Fatalf("expected 1 missing before fix, got %d", report.Missing)
	}

	// Run drift --fix --yes to recreate the missing instance.
	out, err = runCLI(t, "drift", "--fix", "--yes")
	if err != nil {
		t.Fatalf("drift --fix: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Drift remediation result") {
		t.Errorf("expected remediation result; got:\n%s", out)
	}
	if !strings.Contains(out, "Applied:") {
		t.Errorf("expected 'Applied:' section; got:\n%s", out)
	}

	// Verify subsequent drift shows all inSync.
	out, err = runCLI(t, "drift", "--json")
	if err != nil {
		t.Fatalf("post-fix drift: %v\n%s", err, out)
	}
	var reportAfter drift.DriftReport
	if err := json.Unmarshal([]byte(out), &reportAfter); err != nil {
		t.Fatalf("post-fix drift JSON parse: %v\n%s", err, out)
	}

	// All module resources should be in sync now.
	for _, md := range reportAfter.Modules {
		for _, r := range md.Resources {
			if r.Status != drift.InSync {
				t.Errorf("module %q resource %s/%s: status = %q, want %q; details: %s",
					md.Name, r.TypeName, r.Identifier, r.Status, drift.InSync, r.Details)
			}
		}
	}
	if reportAfter.Missing > 0 {
		t.Errorf("expected 0 missing after fix, got %d", reportAfter.Missing)
	}
}

// TestDriftFixDryRunNoChanges: verify --fix --dry-run does not change state.
func TestDriftFixDryRunNoChanges(t *testing.T) {
	store := setupE2E(t)

	if out, err := runCLI(t, "setup", "--yes"); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "horde", "create", "--yes"); err != nil {
		t.Fatalf("horde create: %v\n%s", err, out)
	}

	// Delete the instance from the fake store.
	for id, sr := range store.resources {
		if sr.typeName == "AWS::EC2::Instance" {
			delete(store.resources, id)
			break
		}
	}

	// Run --fix --dry-run — should show plan but not fix.
	out, err := runCLI(t, "drift", "--fix", "--dry-run")
	if err != nil {
		t.Fatalf("drift --fix --dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "--dry-run") {
		t.Errorf("expected dry-run header; got:\n%s", out)
	}

	// Verify drift still shows missing (dry-run should not have fixed it).
	out, err = runCLI(t, "drift", "--json")
	if err != nil {
		t.Fatalf("post dry-run drift: %v\n%s", err, out)
	}
	var report drift.DriftReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("JSON parse: %v\n%s", err, out)
	}
	if report.Missing != 1 {
		t.Errorf("expected 1 missing after dry-run (should not have fixed), got %d", report.Missing)
	}
}
