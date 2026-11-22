package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/naming"
	"github.com/jpvelasco/fabrica/internal/tags"
)

func TestLoadOwnershipValidation(t *testing.T) {
	for _, tc := range []struct {
		owner string
		error string
	}{
		{owner: ""},
		{owner: "fabrica"},
		{owner: "Studio-Tools"},
		{owner: "Fabrica"},
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

func TestLoadNamePrefixGate(t *testing.T) {
	for _, value := range []string{"", "fabrica"} {
		path := writeConfig(t, fmt.Sprintf("cloud:\n  aws:\n    identity:\n      namePrefix: %q\n", value))
		cfg, err := Load(path)
		if err != nil {
			t.Fatalf("namePrefix %q: Load error = %v", value, err)
		}
		prefix, err := cfg.ResolveNamePrefix()
		if err != nil || prefix.String() != naming.DefaultName {
			t.Fatalf("namePrefix %q: prefix = %q, error = %v", value, prefix, err)
		}
	}
	for _, value := range []string{"acme", "Fabrica", "sg"} {
		path := writeConfig(t, fmt.Sprintf("cloud:\n  aws:\n    identity:\n      namePrefix: %q\n", value))
		if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "not supported yet (#705)") {
			t.Fatalf("namePrefix %q: Load error = %v, want the #705 gate", value, err)
		}
	}
}

func TestResolveNamePrefixGateMessage(t *testing.T) {
	cfg := Defaults()
	cfg.Cloud.AWS.Identity.NamePrefix = "acme"
	_, err := cfg.ResolveNamePrefix()
	const want = "cloud.aws.identity.namePrefix is not supported yet (#705); remove it or set it to fabrica"
	if err == nil || err.Error() != want {
		t.Fatalf("ResolveNamePrefix error = %v, want %q", err, want)
	}
	var nilCfg *Config
	if prefix, err := nilCfg.ResolveNamePrefix(); err != nil || prefix != naming.Default {
		t.Fatalf("nil config prefix = %v, error = %v", prefix, err)
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
	if identity, err := cloned.ResolveIdentity(); err != nil || identity.ManagedBy() != "Studio-Tools" {
		t.Fatalf("programmatic custom ownership lost: %v, %v", identity, err)
	}
	path := filepath.Join(t.TempDir(), "roundtrip.yaml")
	if err := cloned.Save(path); err != nil {
		t.Fatal(err)
	}
	if err := cloned.SaveKeys(path, map[string]any{"cloud.aws.region": "us-west-2"}); err != nil {
		t.Fatal(err)
	}
	loaded := loadConfig(t, path)
	if loaded.Cloud.AWS.Identity.ManagedBy != "Studio-Tools" || loaded.Cloud.AWS.Region != "us-west-2" {
		t.Fatalf("save/load lost identity or edited region: %+v", loaded.Cloud.AWS)
	}
}
