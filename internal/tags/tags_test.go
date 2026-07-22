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
