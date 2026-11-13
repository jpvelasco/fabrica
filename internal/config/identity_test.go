package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/tags"
)

func TestLoadOwnershipActivationGuard(t *testing.T) {
	for _, tc := range []struct {
		owner string
		error string
	}{
		{owner: ""},
		{owner: "fabrica"},
		{owner: "Studio-Tools", error: "custom ownership is not enabled"},
		{owner: "Fabrica", error: "custom ownership is not enabled"},
		{owner: " studio", error: "must match"},
		{owner: "studio\n", error: "must match"},
		{owner: "studio/tools", error: "must match"},
		{owner: strings.Repeat("a", 64), error: "must match"},
	} {
		t.Run(tc.owner, func(t *testing.T) {
			path := writeConfig(t, fmt.Sprintf("cloud:\n  aws:\n    identity:\n      managedBy: %q\n", tc.owner))
			_, err := Load(path)
			if tc.error == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.error) {
				t.Fatalf("Load error = %v, want %q", err, tc.error)
			}
		})
	}
}

func TestConfigOwnershipDefaultsAndRoundTrip(t *testing.T) {
	for _, cfg := range []*Config{nil, Defaults()} {
		identity, err := cfg.ResolveIdentity()
		if err != nil || identity.ManagedBy() != tags.ManagedByValue {
			t.Fatalf("default identity = %v, error = %v", identity, err)
		}
	}
	cfg := Defaults()
	cfg.Cloud.AWS.Tags["ManagedBy"] = "operator"
	cfg.Cloud.AWS.VolumeRequestTags = map[string]string{"ManagedBy": "volume-operator"}
	identity, err := cfg.ResolveIdentity()
	if err != nil || identity.ManagedBy() != tags.ManagedByValue {
		t.Fatalf("ordinary tags selected ownership: %v, %v", identity, err)
	}
	cfg.Cloud.AWS.Identity.ManagedBy = "fabrica"
	cloned := cfg.Clone()
	cloned.Cloud.AWS.Identity.ManagedBy = "Studio-Tools"
	if cfg.Cloud.AWS.Identity.ManagedBy != "fabrica" {
		t.Fatal("clone changed original identity")
	}
	out, err := cloned.YAML()
	if err != nil || !strings.Contains(string(out), "managedBy: Studio-Tools") {
		t.Fatalf("identity lost or case changed during serialization: %s, %v", out, err)
	}
	if _, err := cloned.ResolveIdentity(); err == nil {
		t.Fatal("programmatic custom activation was accepted")
	}
	path := filepath.Join(t.TempDir(), "roundtrip.yaml")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SaveKeys(path, map[string]any{"cloud.aws.region": "us-west-2"}); err != nil {
		t.Fatal(err)
	}
	loaded := loadConfig(t, path)
	if loaded.Cloud.AWS.Identity.ManagedBy != "fabrica" || loaded.Cloud.AWS.Region != "us-west-2" {
		t.Fatalf("save/load lost identity or edited region: %+v", loaded.Cloud.AWS)
	}
}
