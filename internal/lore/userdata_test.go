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

func TestApplyDefaults(t *testing.T) {
	t.Run("fills all zeros", func(t *testing.T) {
		cfg := UserDataConfig{}
		cfg.applyDefaults()
		if cfg.StorePath != DefaultStorePath {
			t.Errorf("StorePath = %q, want %q", cfg.StorePath, DefaultStorePath)
		}
		if cfg.ConfigDir != DefaultConfigDir {
			t.Errorf("ConfigDir = %q, want %q", cfg.ConfigDir, DefaultConfigDir)
		}
		if cfg.GRPCPort != DefaultGRPCPort {
			t.Errorf("GRPCPort = %d, want %d", cfg.GRPCPort, DefaultGRPCPort)
		}
		if cfg.HTTPPort != DefaultHTTPPort {
			t.Errorf("HTTPPort = %d, want %d", cfg.HTTPPort, DefaultHTTPPort)
		}
	})
	t.Run("preserves existing values", func(t *testing.T) {
		cfg := UserDataConfig{
			StorePath: "/custom/store",
			ConfigDir: "/custom/config",
			GRPCPort:  9999,
			HTTPPort:  9998,
		}
		cfg.applyDefaults()
		if cfg.StorePath != "/custom/store" {
			t.Errorf("StorePath = %q, want /custom/store", cfg.StorePath)
		}
		if cfg.ConfigDir != "/custom/config" {
			t.Errorf("ConfigDir = %q, want /custom/config", cfg.ConfigDir)
		}
		if cfg.GRPCPort != 9999 {
			t.Errorf("GRPCPort = %d, want 9999", cfg.GRPCPort)
		}
		if cfg.HTTPPort != 9998 {
			t.Errorf("HTTPPort = %d, want 9998", cfg.HTTPPort)
		}
	})
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
