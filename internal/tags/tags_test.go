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

func TestIdentityKey(t *testing.T) {
	for _, k := range []string{"ManagedBy", "FabricaModule", "FabricaVersion", "FabricaCreateNonce"} {
		if !IdentityKey(k) {
			t.Errorf("IdentityKey(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"Project", "Name", "managedBy", ""} {
		if IdentityKey(k) {
			t.Errorf("IdentityKey(%q) = true, want false", k)
		}
	}
}

func TestMergeProtected(t *testing.T) {
	base := map[string]string{"Name": "fabrica-perforce-sg", "FabricaModule": "perforce"}
	extra := map[string]string{
		"Project":   "studio",
		"ManagedBy": "custom",
		"":          "dropped",
	}
	got := MergeProtected("perforce", "0.5.0", base, extra)

	if got["Name"] != "fabrica-perforce-sg" {
		t.Errorf("base tag not preserved: %v", got)
	}
	if got["ManagedBy"] != "fabrica" {
		t.Errorf("standard ManagedBy must override base and extra, got %q", got["ManagedBy"])
	}
	if got["FabricaVersion"] != "0.5.0" {
		t.Errorf("standard FabricaVersion forced, got %q", got["FabricaVersion"])
	}
	if got["FabricaModule"] != "perforce" {
		t.Errorf("base FabricaModule must survive, got %q", got["FabricaModule"])
	}
	if got["Project"] != "studio" {
		t.Errorf("extra tag not merged: %v", got)
	}
	if _, has := got[""]; has {
		t.Errorf("empty key must be dropped: %v", got)
	}
	if len(got) != 5 {
		t.Errorf("len(tags) = %d, want 5 (Name, ManagedBy, FabricaVersion, FabricaModule, Project): %v", len(got), got)
	}
}

func TestMergeProtectedNoBase(t *testing.T) {
	got := MergeProtected("fabrica", "test-version", nil, map[string]string{"CostCenter": "eng"})
	want := map[string]string{
		"ManagedBy":      "fabrica",
		"FabricaModule":  "fabrica",
		"FabricaVersion": "test-version",
		"CostCenter":     "eng",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("tag %s = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("len(tags) = %d, want %d: %v", len(got), len(want), got)
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
