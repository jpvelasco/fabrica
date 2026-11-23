package e2e

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
	"github.com/jpvelasco/fabrica/internal/state"
)

// TestDestroyAllFullStack: provision perforce + horde, aggregate cost report,
// then destroy --all removes every module and the backend.
func TestDestroyAllFullStack(t *testing.T) {
	store := setupE2E(t)

	if out, err := runCLI(t, "setup", "--yes"); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "perforce", "create", "--yes"); err != nil {
		t.Fatalf("perforce create: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "horde", "create", "--yes"); err != nil {
		t.Fatalf("horde create: %v\n%s", err, out)
	}

	st := readState(t)
	assertModuleExists(t, st, "perforce")
	assertModuleExists(t, st, "horde")

	// Aggregate cost report covers both modules.
	out, err := runCLI(t, "cost", "report")
	if err != nil {
		t.Fatalf("cost report: %v\n%s", err, out)
	}
	assert.Contains(t, out, "perforce")
	assert.Contains(t, out, "horde")
	assert.Contains(t, out, "Total:")

	// Full teardown.
	out, err = runCLI(t, "destroy", "--all", "--yes")
	if err != nil {
		t.Fatalf("destroy --all: %v\n%s", err, out)
	}
	assert.Contains(t, out, "Destroy --all complete")

	// Every module gone from state.
	st = readState(t)
	assertModuleAbsent(t, st, "perforce")
	assertModuleAbsent(t, st, "horde")

	// The state bucket, its state document, and the lock table are gone with
	// no manual step.
	if len(store.buckets) != 0 || len(store.stateDocs) != 0 || len(store.tables) != 0 {
		t.Fatalf("backend not removed: buckets=%v stateDocs=%d tables=%v", store.buckets, len(store.stateDocs), store.tables)
	}
}

// TestDestroyAllRefusesForeignStateBucketObject: an object Fabrica did not write
// sits beside the state document. destroy --all tears the modules down, then
// refuses the bucket, names the foreign key, and keeps the backend.
func TestDestroyAllRefusesForeignStateBucketObject(t *testing.T) {
	store := setupE2E(t)

	if out, err := runCLI(t, "setup", "--yes"); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "perforce", "create", "--yes"); err != nil {
		t.Fatalf("perforce create: %v\n%s", err, out)
	}
	var bucket string
	for name := range store.buckets {
		bucket = name
	}
	store.stateDocs[bucket+"/user/notes.txt"] = "not fabrica's"

	out, err := runCLI(t, "destroy", "--all", "--yes")
	if err == nil {
		t.Fatalf("expected destroy --all to refuse a bucket with a foreign object:\n%s", out)
	}
	assert.Contains(t, out, "user/notes.txt")
	if !store.buckets[bucket] {
		t.Fatal("state bucket must be kept when it holds a foreign object")
	}
	if _, ok := store.stateDocs[bucket+"/"+state.StateDocumentKey]; !ok {
		t.Fatal("state document must survive a refused bucket delete")
	}
	if _, ok := store.stateDocs[bucket+"/user/notes.txt"]; !ok {
		t.Fatal("foreign object must not be deleted")
	}
}

// TestDestroyAllModuleFailurePreservesBackend: when a module's resource deletion
// fails, destroy --all continues, returns an error naming the failed module, and
// does NOT delete the state backend (the backend-only-on-full-success invariant).
func TestDestroyAllModuleFailurePreservesBackend(t *testing.T) {
	store := setupE2E(t)

	if out, err := runCLI(t, "setup", "--yes"); err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	if out, err := runCLI(t, "perforce", "create", "--yes"); err != nil {
		t.Fatalf("perforce create: %v\n%s", err, out)
	}

	// Force perforce's instance deletion to fail.
	store.failDeleteType = "AWS::EC2::Instance"

	out, err := runCLI(t, "destroy", "--all", "--yes")
	if err == nil {
		t.Fatalf("expected destroy --all to error when a module fails:\n%s", out)
	}
	// The failure summary names the failed module.
	if !strings.Contains(out, "perforce") {
		t.Fatalf("failure output should name the failed module:\n%s", out)
	}

	// Backend preserved: the fake still has the bucket + table (setup created
	// them; a failed teardown must not remove them).
	if len(store.buckets) == 0 || len(store.tables) == 0 {
		t.Fatal("state backend must be preserved when a module teardown fails")
	}
}
