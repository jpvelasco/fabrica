package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/credentials"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

// TestPerforceBackupRestoreFlow exercises backup create → list → status → restore dry-run → delete.
func TestPerforceBackupRestoreFlow(t *testing.T) {
	store := setupE2E(t)

	out, err := runCLI(t, "perforce", "create", "--yes")
	if err != nil {
		t.Fatalf("create: %v\n%s", err, out)
	}

	st := readState(t)
	m := st.GetModule("perforce")
	if m == nil {
		t.Fatal("perforce module missing after create")
	}
	for i := range st.Modules {
		if st.Modules[i].Name == "perforce" {
			st.Modules[i].Status = "ready"
		}
	}
	if err := fabricastate.WriteState(st); err != nil {
		t.Fatalf("write state ready: %v", err)
	}

	if err := credentials.WriteCredentials(
		filepath.Join(".fabrica", "perforce-credentials.yaml"),
		credentials.FormatPerforce("testpass"),
	); err != nil {
		t.Fatal(err)
	}

	store.listStdout = `{"id":"20260715-143022-smoke","status":"complete","createdAt":"2026-07-15T14:30:22Z","sizeBytes":42,"helixVersion":"2024.2","serverRoot":"/hxdepots"}` + "\n"

	out, err = runCLI(t, "perforce", "backup", "--yes", "--name", "smoke")
	if err != nil {
		t.Fatalf("backup: %v\n%s", err, out)
	}
	assertContains(t, out, "Backup complete")

	out, err = runCLI(t, "perforce", "backup", "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, out)
	}
	assertContains(t, out, "20260715-143022-smoke")

	out, err = runCLI(t, "perforce", "status")
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	assertContains(t, out, "Last backup")

	out, err = runCLI(t, "perforce", "restore", "fake-backup", "--force", "--dry-run")
	if err != nil {
		t.Fatalf("restore dry-run: %v\n%s", err, out)
	}
	assertContains(t, out, "dry run")

	out, err = runCLI(t, "perforce", "backup", "delete", "20260715-143022-smoke", "--yes")
	if err != nil {
		t.Fatalf("delete: %v\n%s", err, out)
	}
	assertContains(t, out, "Deleted backup")

	out, err = runCLI(t, "perforce", "destroy", "--dry-run")
	if err != nil {
		t.Fatalf("destroy dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "AWS::EC2::Instance") && !strings.Contains(out, "destroy") {
		t.Fatalf("unexpected destroy dry-run output:\n%s", out)
	}
}
