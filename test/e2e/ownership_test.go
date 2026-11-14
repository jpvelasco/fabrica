package e2e

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/drift"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/jpvelasco/fabrica/internal/tags"
)

func TestCustomOwnershipLifecycle(t *testing.T) {
	store := setupE2E(t)
	cfg, err := config.Load("fabrica.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Cloud.AWS.Identity.ManagedBy = "Studio-Tools"
	cfg.Cloud.AWS.Tags = map[string]string{"ManagedBy": "forged", "FabricaOwnershipVersion": "forged", "Project": "studio"}
	if err := cfg.Save("fabrica.yaml"); err != nil {
		t.Fatal(err)
	}
	commands := [][]string{{"setup", "--dry-run"}, {"setup", "--yes"}, {"perforce", "create", "--yes"}, {"horde", "create", "--yes"}, {"lore", "create", "--yes"}, {"ddc", "setup", "--yes"}, {"workstation", "create", "--yes"}, {"ci", "setup", "--yes"}, {"deploy", "setup", "--yes"}, {"deploy", "promote", "test-build", "--no-wait", "--yes"}}
	for _, args := range commands {
		if out, err := runCLI(t, args...); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	for _, metadata := range []map[string]string{store.bucketTags[cfg.State.Bucket], store.tableTags[cfg.State.Table]} {
		if metadata[tags.ManagedByKey] != "Studio-Tools" || metadata[tags.OwnershipVersionKey] != "1" || metadata["Project"] != "studio" {
			t.Fatalf("wrong backend tags: %v", metadata)
		}
	}
	for id, res := range store.resources {
		if res.tags[tags.ManagedByKey] != "Studio-Tools" {
			t.Fatalf("%s has wrong owner: %v", id, res.tags)
		}
	}
	canonical := store.stateDocs[cfg.State.Bucket+"/"+state.StateDocumentKey]
	if !strings.Contains(canonical, `"managedBy": "Studio-Tools"`) {
		t.Fatal("custom owner not persisted")
	}
	// A fresh directory recovers the bound owner and the full remote inventory.
	if err := os.Remove(".fabrica/state.json"); err != nil {
		t.Fatal(err)
	}
	if out, err := runCLI(t, "status"); err != nil {
		t.Fatalf("remote recovery: %v\n%s", err, out)
	}
	if readState(t).ManagedBy != "Studio-Tools" {
		t.Fatal("remote recovery lost binding")
	}
	store.resources["custom-orphan"] = &storedResource{typeName: "AWS::EC2::SecurityGroup", identifier: "custom-orphan", tags: map[string]string{"ManagedBy": "Studio-Tools"}}
	store.resources["default-orphan"] = &storedResource{typeName: "AWS::EC2::SecurityGroup", identifier: "default-orphan", tags: map[string]string{"ManagedBy": "fabrica"}}
	out, err := runCLI(t, "drift", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var report drift.DriftReport
	assertJSON(t, out, &report)
	if report.Extra != 1 {
		t.Fatalf("drift mixed owners: %s", out)
	}
	delete(store.resources, "custom-orphan")
	for _, format := range []string{"cloudformation", "terraform"} {
		if out, err := runCLI(t, "export", "--format", format); err != nil || !strings.Contains(out, "Studio-Tools") {
			t.Fatalf("export %s: %v\n%s", format, err, out)
		}
	}
	before := len(store.resources)
	if err := cfg.SaveKeys("fabrica.yaml", map[string]any{"cloud.aws.identity.managedBy": "other"}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"setup", "--yes"}, {"perforce", "create", "--yes"}, {"destroy", "--all", "--yes"}, {"export", "--format", "cloudformation"}, {"drift"}} {
		if _, err := runCLI(t, args...); err == nil || !strings.Contains(err.Error(), "ownership") {
			t.Fatalf("%v allowed ownership change: %v", args, err)
		}
	}
	if len(store.resources) != before || store.stateDocs[cfg.State.Bucket+"/"+state.StateDocumentKey] != canonical {
		t.Fatal("ownership change mutated deployment")
	}
	if err := cfg.SaveKeys("fabrica.yaml", map[string]any{"cloud.aws.identity.managedBy": "Studio-Tools"}); err != nil {
		t.Fatal(err)
	}
	if out, err := runCLI(t, "destroy", "--all", "--yes"); err != nil {
		t.Fatalf("destroy: %v\n%s", err, out)
	}
	if len(store.resources) != 1 || store.resources["default-orphan"] == nil {
		data, _ := json.Marshal(store.resources)
		t.Fatalf("teardown removed foreign resource or missed recorded resources: %s", data)
	}
	if store.buckets[cfg.State.Bucket] || store.tables[cfg.State.Table] {
		t.Fatal("teardown retained custom backend")
	}
}
