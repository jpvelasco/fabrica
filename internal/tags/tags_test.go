package tags

import "testing"

func TestStandard(t *testing.T) {
	got := Standard("perforce", "2024.2")

	if got["ManagedBy"] != "fabrica" {
		t.Errorf("ManagedBy = %q", got["ManagedBy"])
	}
	if got["FabricaModule"] != "perforce" {
		t.Errorf("FabricaModule = %q", got["FabricaModule"])
	}
	if got["FabricaVersion"] != "2024.2" {
		t.Errorf("FabricaVersion = %q", got["FabricaVersion"])
	}
	if len(got) != 3 {
		t.Errorf("len(tags) = %d, want 3", len(got))
	}
}

func TestMerge(t *testing.T) {
	got := Merge("setup", "test-version", map[string]string{
		"Project":   "studio",
		"ManagedBy": "custom",
	})

	if got["Project"] != "studio" {
		t.Errorf("extra tag not merged: %v", got)
	}
	if got["ManagedBy"] != "custom" {
		t.Errorf("extra tag should override standard tag, got %q", got["ManagedBy"])
	}
	if got["FabricaModule"] != "setup" || got["FabricaVersion"] != "test-version" {
		t.Errorf("standard tags lost after merge: %v", got)
	}
	if len(got) != 4 {
		t.Errorf("len(tags) = %d, want 4", len(got))
	}

	empty := Merge("lore", "1.0.0", nil)
	if len(empty) != 3 || empty["FabricaModule"] != "lore" {
		t.Errorf("nil extra should yield standard tags only: %v", empty)
	}
}

func TestStandardEmptyValues(t *testing.T) {
	got := Standard("", "")

	if got["ManagedBy"] != "fabrica" {
		t.Error("ManagedBy should always be fabrica")
	}
	if got["FabricaModule"] != "" {
		t.Error("FabricaModule should be empty")
	}
	if got["FabricaVersion"] != "" {
		t.Error("FabricaVersion should be empty")
	}
}
