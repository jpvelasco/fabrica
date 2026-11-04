package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// TestWorkstationPasswordIsNotInUserData: create stores the DCV password in
// the parameter seam, not in Cloud Control desired state, and terminate
// deletes that parameter.
func TestWorkstationPasswordIsNotInUserData(t *testing.T) {
	store := setupE2E(t)

	out, err := runCLI(t, "workstation", "create", "--yes")
	if err != nil {
		t.Fatalf("workstation create: %v\n%s", err, out)
	}
	if len(store.parameters) != 1 {
		t.Fatalf("parameters = %d, want 1", len(store.parameters))
	}
	var secret string
	for _, value := range store.parameters {
		secret = value
	}
	if secret == "" {
		t.Fatal("stored an empty session password")
	}
	for id, sr := range store.resources {
		if strings.Contains(string(sr.desired), secret) {
			t.Fatalf("resource %s desired state contains the session password", id)
		}
	}
	stateFile, err := os.ReadFile(".fabrica/state.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stateFile), secret) {
		t.Fatal("state file contains the session password")
	}

	out, err = runCLI(t, "workstation", "terminate", "--yes")
	if err != nil {
		t.Fatalf("workstation terminate: %v\n%s", err, out)
	}
	if len(store.parameters) != 0 {
		t.Fatalf("terminate left parameters: %v", store.parameters)
	}
}

// TestWorkstationRootVolumeEncrypted: the instance create carries one block
// device mapping, on the root device the AMI reports (the fake reports
// /dev/sda1), with Ebs.Encrypted=true (#507).
func TestWorkstationRootVolumeEncrypted(t *testing.T) {
	store := setupE2E(t)

	out, err := runCLI(t, "workstation", "create", "--yes")
	if err != nil {
		t.Fatalf("workstation create: %v\n%s", err, out)
	}
	found := false
	for id, sr := range store.resources {
		if sr.typeName != "AWS::EC2::Instance" {
			continue
		}
		found = true
		var doc struct {
			BlockDeviceMappings []struct {
				DeviceName string
				Ebs        struct{ Encrypted *bool }
			}
		}
		if err := json.Unmarshal(sr.desired, &doc); err != nil {
			t.Fatalf("instance %s desired state: %v", id, err)
		}
		if len(doc.BlockDeviceMappings) != 1 || doc.BlockDeviceMappings[0].DeviceName != "/dev/sda1" {
			t.Fatalf("instance %s mappings = %+v, want only the /dev/sda1 root", id, doc.BlockDeviceMappings)
		}
		if enc := doc.BlockDeviceMappings[0].Ebs.Encrypted; enc == nil || !*enc {
			t.Fatalf("instance %s root Ebs.Encrypted = %v, want true", id, enc)
		}
	}
	if !found {
		t.Fatal("no EC2 instance was created")
	}
}

// TestWorkstationRunInstancesFallbackEncryptedRoot: when Cloud Control cannot
// tag the root volume on the create request, the provider falls back to a
// direct RunInstances. That request must carry exactly one block device
// mapping — the AMI's root device, as the image inspector reports it — with
// Ebs.Encrypted=true (#512). The e2e fake simulates the volume-tag deny and
// captures the request built by the provider's real builder.
func TestWorkstationRunInstancesFallbackEncryptedRoot(t *testing.T) {
	for _, root := range []string{"/dev/sda1", "/dev/xvda"} {
		t.Run(strings.TrimPrefix(root, "/dev/"), func(t *testing.T) {
			store := setupE2E(t)
			store.imageRootDevice = root
			store.volumeTagDeny = true

			out, err := runCLI(t, "workstation", "create", "--yes")
			if err != nil {
				t.Fatalf("workstation create: %v\n%s", err, out)
			}
			assertModuleExists(t, readState(t), "workstation")

			in := store.lastRunInstances
			if in == nil {
				t.Fatal("no RunInstances request was captured: the fallback did not run")
			}
			if len(in.BlockDeviceMappings) != 1 {
				t.Fatalf("BlockDeviceMappings = %d, want exactly one (the root volume)", len(in.BlockDeviceMappings))
			}
			m := in.BlockDeviceMappings[0]
			if m.DeviceName == nil || *m.DeviceName != root {
				t.Fatalf("device = %v, want the AMI root device %s", m.DeviceName, root)
			}
			if m.Ebs == nil || m.Ebs.Encrypted == nil || !*m.Ebs.Encrypted {
				t.Fatalf("root Ebs.Encrypted = %+v, want true", m.Ebs)
			}
		})
	}
}

// TestWorkstationTerminateRemovesCredentialsFile: create writes the local DCV
// credentials record, and a fully successful teardown removes it (#511). This
// drives the real command tree, so the teardown engine's real os.Remove wiring
// runs end to end.
func TestWorkstationTerminateRemovesCredentialsFile(t *testing.T) {
	setupE2E(t)

	credsPath := filepath.Join(".fabrica", "workstation-credentials.yaml")
	out, err := runCLI(t, "workstation", "create", "--yes")
	if err != nil {
		t.Fatalf("workstation create: %v\n%s", err, out)
	}
	if _, err := os.Stat(credsPath); err != nil {
		t.Fatalf("create should have written %s: %v", credsPath, err)
	}

	out, err = runCLI(t, "workstation", "terminate", "--yes")
	if err != nil {
		t.Fatalf("workstation terminate: %v\n%s", err, out)
	}
	// The engine prints the constant's forward-slash path regardless of the
	// platform separator in credsPath.
	assert.Contains(t, out, "Removed: .fabrica/workstation-credentials.yaml")
	if _, err := os.Stat(credsPath); !os.IsNotExist(err) {
		t.Fatalf("terminate left %s on disk: %v", credsPath, err)
	}
}
