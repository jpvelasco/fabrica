package e2e

import (
	"errors"
	"os"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
)

const remoteStateBucket = "fabrica-state-123456789012"

// TestRemoteStateCanonical drives the full CLI surface: the remote state
// document is the record of truth, so a second working directory with no
// local cache reads the inventory from the remote backend — and remote
// storage failures surface instead of being silently absorbed.
func TestRemoteStateCanonical(t *testing.T) {
	store := setupE2E(t)

	// Bootstrap the state backend so the fake has the state bucket.
	out, err := runCLI(t, "setup", "--yes")
	if err != nil {
		t.Fatalf("setup: %v\n%s", err, out)
	}
	assert.Contains(t, out, "Setup complete")

	// Provision a module in dir A; the inventory must land remotely.
	out, err = runCLI(t, "lore", "create", "--yes")
	if err != nil {
		t.Fatalf("lore create: %v\n%s", err, out)
	}

	key := remoteStateBucket + "/state.json"
	store.mu.Lock()
	doc, ok := store.stateDocs[key]
	store.mu.Unlock()
	if !ok || doc == "" {
		t.Fatalf("remote state document not written; docs = %v", store.stateDocs)
	}

	t.Run("second directory reads inventory from remote", func(t *testing.T) {
		t.Chdir(t.TempDir())
		writeConfig(t) // same config, fresh working directory

		if _, err := os.Stat(".fabrica/state.json"); !os.IsNotExist(err) {
			t.Fatalf("precondition: no local cache in the new directory, got %v", err)
		}

		out, err := runCLI(t, "status")
		if err != nil {
			t.Fatalf("status: %v\n%s", err, out)
		}
		assert.Contains(t, out, "lore")

		// The status read must also refresh the local cache from the remote.
		st := readState(t)
		assertModuleExists(t, st, "lore")
		assertResourceType(t, st, "lore", "AWS::EC2::Instance")

		out, err = runCLI(t, "cost", "report")
		if err != nil {
			t.Fatalf("cost report: %v\n%s", err, out)
		}
		assert.Contains(t, out, "lore")
	})

	t.Run("remote read failure surfaces", func(t *testing.T) {
		store.stateStoreGetErr = errors.New("s3 unavailable")
		defer func() { store.stateStoreGetErr = nil }()

		out, err := runCLI(t, "status")
		if err == nil {
			t.Fatal("expected status to fail on a remote read error")
		}
		assert.Contains(t, out, "reading remote state")
	})

	t.Run("remote write failure fails the mutation and keeps the cache", func(t *testing.T) {
		store.stateStorePutErr = errors.New("s3 put failed")
		defer func() { store.stateStorePutErr = nil }()

		out, err := runCLI(t, "perforce", "create", "--yes")
		if err == nil {
			t.Fatalf("expected perforce create to fail on a remote write error\n%s", out)
		}
		assert.Contains(t, out, "persisting remote state")

		st := readState(t)
		assertModuleAbsent(t, st, "perforce")
	})

	t.Run("bucket absent degrades to local only", func(t *testing.T) {
		store.mu.Lock()
		delete(store.buckets, remoteStateBucket)
		store.mu.Unlock()

		out, err := runCLI(t, "perforce", "create", "--yes")
		if err != nil {
			t.Fatalf("perforce create must degrade to local-only when the state bucket is absent: %v\n%s", err, out)
		}

		st := readState(t)
		assertModuleExists(t, st, "perforce")
	})
}
