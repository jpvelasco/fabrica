package e2e

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
)

// writeFabricaConfig emits a minimal fake-provider config file targeting the
// given account/region, so a profile or a rebound config runs against the same
// in-memory store as the default config.
func writeFabricaConfig(t *testing.T, file, account, region string) {
	t.Helper()
	cfg := "cloud:\n" +
		"  provider: fake\n" +
		"  aws:\n" +
		"    region: " + region + "\n" +
		"    accountId: \"" + account + "\"\n" +
		"state:\n" +
		"  bucket: fabrica-state-" + account + "\n" +
		"  table: fabrica-state-lock\n" +
		"perforce:\n" +
		"  vpcId: vpc-fake\n" +
		"  subnetId: subnet-fake\n"
	if err := os.WriteFile(file, []byte(cfg), 0600); err != nil {
		t.Fatalf("writeFabricaConfig(%s): %v", file, err)
	}
}

// TestProfileStateIsolation: two configs in one working directory keep
// separate local state files — a default-profile create must not leak into the
// prod profile's cache, and vice versa.
func TestProfileStateIsolation(t *testing.T) {
	setupE2E(t)

	out, err := runCLI(t, "perforce", "create", "--yes")
	if err != nil {
		t.Fatalf("perforce create: %v\n%s", err, out)
	}
	assert.Contains(t, out, "Perforce Helix Core provisioned.")

	defSt := readState(t)
	assertModuleExists(t, defSt, "perforce")
	if defSt.Account != "123456789012" {
		t.Fatalf("default state account = %q, want 123456789012", defSt.Account)
	}
	before, err := os.ReadFile(".fabrica/state.json")
	if err != nil {
		t.Fatalf("read default state file: %v", err)
	}

	writeFabricaConfig(t, "fabrica-prod.yaml", "999999999999", "eu-west-1")
	out, err = runCLI(t, "perforce", "create", "--yes", "--profile", "prod")
	if err != nil {
		t.Fatalf("perforce create --profile prod: %v\n%s", err, out)
	}
	assert.Contains(t, out, "Perforce Helix Core provisioned.")

	prodSt := loadStateFile(t, ".fabrica/state-prod.json")
	assertModuleExists(t, prodSt, "perforce")
	if prodSt.Account != "999999999999" || prodSt.Region != "eu-west-1" {
		t.Errorf("prod state account/region = %q/%q, want 999999999999/eu-west-1", prodSt.Account, prodSt.Region)
	}

	after, err := os.ReadFile(".fabrica/state.json")
	if err != nil {
		t.Fatalf("re-read default state file: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("default state file was modified by the prod-profile create")
	}
}

// TestStateMismatchAccountSwitch: the same config file rebound to a different
// account must have its existing local state rejected before any mutation.
func TestStateMismatchAccountSwitch(t *testing.T) {
	setupE2E(t)

	if _, err := runCLI(t, "perforce", "create", "--yes"); err != nil {
		t.Fatalf("perforce create: %v", err)
	}

	writeFabricaConfig(t, "fabrica.yaml", "999999999999", "us-east-1")
	out, err := runCLI(t, "perforce", "status")
	if err == nil {
		t.Fatalf("expected state mismatch error, got nil\n%s", out)
	}
	assert.Contains(t, out, "state target mismatch")
	assert.Contains(t, out, "123456789012")
	assert.Contains(t, out, "999999999999")

	// The state file is untouched — rejection happens before any mutation.
	st := readState(t)
	if st.Account != "123456789012" {
		t.Errorf("state account = %q, want 123456789012", st.Account)
	}
	assertModuleExists(t, st, "perforce")
}

// TestStateMismatchRegionSwitch: the same config file rebound to a different
// home region must have its existing local state rejected before any mutation.
func TestStateMismatchRegionSwitch(t *testing.T) {
	setupE2E(t)

	if _, err := runCLI(t, "perforce", "create", "--yes"); err != nil {
		t.Fatalf("perforce create: %v", err)
	}

	writeFabricaConfig(t, "fabrica.yaml", "123456789012", "eu-west-1")
	out, err := runCLI(t, "perforce", "status")
	if err == nil {
		t.Fatalf("expected state mismatch error, got nil\n%s", out)
	}
	assert.Contains(t, out, "state target mismatch")
	assert.Contains(t, out, "us-east-1")
	assert.Contains(t, out, "eu-west-1")
}

// TestStateMismatchDeletionRebinds: deleting the conflicting state file is the
// documented recovery path — the next command re-seeds it for the new target.
func TestStateMismatchDeletionRebinds(t *testing.T) {
	setupE2E(t)

	if _, err := runCLI(t, "perforce", "create", "--yes"); err != nil {
		t.Fatalf("perforce create: %v", err)
	}

	writeFabricaConfig(t, "fabrica.yaml", "999999999999", "us-east-1")
	if err := os.Remove(".fabrica/state.json"); err != nil {
		t.Fatalf("remove state file: %v", err)
	}

	out, err := runCLI(t, "perforce", "status")
	if err != nil {
		t.Fatalf("perforce status after rebind: %v\n%s", err, out)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("expected status output, got empty")
	}
}
