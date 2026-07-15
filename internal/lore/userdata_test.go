package lore

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateRawContainsStoreAndPorts(t *testing.T) {
	raw, err := GenerateRaw(UserDataConfig{
		StorePath: "/opt/loreserver/store",
		ConfigDir: "/etc/loreserver",
		GRPCPort:  41337,
		HTTPPort:  41339,
	})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	for _, want := range []string{
		"/opt/loreserver/store",
		"/etc/loreserver",
		"local.toml",
		"loreserver",
		"41337",
		"41339",
		"fabrica-lore-init.log",
		"resolve_data_dev",
		"/dev/nvme",
		"/dev/sdf",
		"/dev/xvdf",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("userdata missing %q", want)
		}
	}
}

func TestGenerateRawDefaults(t *testing.T) {
	raw, err := GenerateRaw(UserDataConfig{})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	if !strings.Contains(raw, DefaultStorePath) {
		t.Errorf("missing default store path")
	}
	if !strings.Contains(raw, DefaultConfigDir) {
		t.Errorf("missing default config dir")
	}
}

func TestGenerateBase64RoundTrip(t *testing.T) {
	encoded, err := Generate(UserDataConfig{StorePath: "/data", ConfigDir: "/cfg"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("not valid base64: %v", err)
	}
	if !strings.Contains(string(decoded), "/data") {
		t.Error("decoded userdata missing /data")
	}
}
