package tags

import (
	"maps"
	"strings"
	"testing"
)

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
	for _, k := range []string{"ManagedBy", "FabricaModule", "FabricaVersion", "FabricaCreateNonce", "FabricaOwnershipVersion"} {
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

func TestResolveIdentity(t *testing.T) {
	for _, value := range []string{"", "fabrica", "studio-tools", "Studio_Tools.v1", "7", strings.Repeat("a", 63)} {
		t.Run(value, func(t *testing.T) {
			identity, err := ResolveIdentity(value)
			if err != nil {
				t.Fatalf("ResolveIdentity(%q): %v", value, err)
			}
			want := value
			if want == "" {
				want = ManagedByValue
			}
			if identity.ManagedBy() != want {
				t.Fatalf("owner = %q, want %q", identity.ManagedBy(), want)
			}
			if got := identity.Standard("horde", "test"); got[ManagedByKey] != want || got["FabricaModule"] != "horde" || got["FabricaVersion"] != "test" {
				t.Fatalf("standard tags = %v", got)
			}
		})
	}
	for _, value := range []string{" studio", "studio ", "studio tools", "studio\n", "studio\x00", "studio/tools", "studio:tools", "studio+tools", ".studio", "-studio", "_studio", "stúdio", strings.Repeat("a", 64)} {
		t.Run(value, func(t *testing.T) {
			if _, err := ResolveIdentity(value); err == nil {
				t.Fatalf("ResolveIdentity(%q) accepted invalid owner", value)
			}
		})
	}
}

func TestIdentityMergeProtected(t *testing.T) {
	identity, err := ResolveIdentity("Studio-Tools")
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]string{
		ManagedByKey:              "foreign",
		"FabricaModule":           "horde",
		"FabricaVersion":          "plan-version",
		"FabricaCreateNonce":      "trusted-nonce",
		"FabricaOwnershipVersion": "1",
		"FabricaRole":             "agent",
		"Name":                    "fabrica-agent",
		"Project":                 "base-project",
	}
	extra := map[string]string{
		ManagedByKey:              "operator",
		"FabricaModule":           "operator",
		"FabricaVersion":          "operator",
		"FabricaCreateNonce":      "operator",
		"FabricaOwnershipVersion": "operator",
		"Project":                 "operator-project",
		"":                        "invalid-key",
	}
	baseBefore, extraBefore := maps.Clone(base), maps.Clone(extra)
	got := identity.MergeProtected(base, extra)
	want := maps.Clone(base)
	want[ManagedByKey] = "Studio-Tools"
	want["Project"] = "operator-project"
	if !maps.Equal(got, want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
	got["Name"] = "changed"
	if !maps.Equal(base, baseBefore) || !maps.Equal(extra, extraBefore) {
		t.Fatal("merge mutated caller maps")
	}
}

func TestIdentityMergeDoesNotForgeReservedTags(t *testing.T) {
	var identity Identity
	extra := map[string]string{"Project": "studio"}
	for _, key := range []string{"ManagedBy", "FabricaModule", "FabricaVersion", "FabricaCreateNonce", "FabricaOwnershipVersion"} {
		extra[key] = "operator"
	}
	got := identity.MergeProtected(nil, extra)
	want := map[string]string{ManagedByKey: ManagedByValue, "Project": "studio"}
	if !maps.Equal(got, want) {
		t.Fatalf("tags = %v, want %v", got, want)
	}
}

func TestIdentitiesRemainIndependent(t *testing.T) {
	for _, owner := range []string{"studio-a", "studio-b", ManagedByValue} {
		t.Run(owner, func(t *testing.T) {
			t.Parallel()
			identity, err := ResolveIdentity(owner)
			if err != nil {
				t.Fatal(err)
			}
			for range 100 {
				if got := identity.MergeProtected(nil, nil)[ManagedByKey]; got != owner {
					t.Fatalf("owner = %q, want %q", got, owner)
				}
			}
		})
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

func TestIdentityMergeStandardPreservesTrustedMetadata(t *testing.T) {
	for _, owner := range []string{"fabrica", "Studio-A"} {
		identity, err := ResolveIdentity(owner)
		if err != nil {
			t.Fatal(err)
		}
		base := map[string]string{ManagedByKey: "foreign", "FabricaModule": "lore", "FabricaVersion": "old",
			"FabricaCreateNonce": "trusted", "Project": "base"}
		extra := map[string]string{ManagedByKey: "forged", "FabricaModule": "forged", "FabricaVersion": "forged",
			"FabricaCreateNonce": "forged", OwnershipVersionKey: "forged", "Project": "operator"}
		beforeBase, beforeExtra := maps.Clone(base), maps.Clone(extra)
		got := identity.MergeStandard("fallback", "new", base, extra)
		want := maps.Clone(base)
		want[ManagedByKey], want["FabricaVersion"], want["Project"] = owner, "new", "operator"
		if !maps.Equal(got, want) || !maps.Equal(base, beforeBase) || !maps.Equal(extra, beforeExtra) {
			t.Fatalf("got=%v want=%v base=%v extra=%v", got, want, base, extra)
		}
		fallback := identity.MergeStandard("fallback", "new", nil, nil)
		if !maps.Equal(fallback, identity.Standard("fallback", "new")) {
			t.Fatalf("fallback=%v", fallback)
		}
		if owner == ManagedByValue && !maps.Equal(got, MergeProtected("fallback", "new", base, extra)) {
			t.Fatal("default adapter changed")
		}
	}
}
