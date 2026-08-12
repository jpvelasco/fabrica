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
	// Default backend is local.
	if !strings.Contains(raw, `mode = "local"`) {
		t.Errorf("expected local store mode in default userdata")
	}
}

func TestGenerateRawS3StoreBackend(t *testing.T) {
	raw, err := GenerateRaw(UserDataConfig{
		StorePath:    "/opt/loreserver/store",
		ConfigDir:    "/etc/loreserver",
		GRPCPort:     41337,
		HTTPPort:     41339,
		StoreBackend: StoreBackendS3,
		StoreBucket:  "my-lore-bucket",
	})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}

	// S3 store config should be present.
	if !strings.Contains(raw, `mode = "s3"`) {
		t.Errorf("expected s3 store mode in userdata")
	}
	if !strings.Contains(raw, `bucket = "my-lore-bucket"`) {
		t.Errorf("expected S3 bucket name in userdata")
	}
	if !strings.Contains(raw, `prefix = "immutable"`) {
		t.Errorf("expected immutable prefix in userdata")
	}
	if !strings.Contains(raw, `prefix = "mutable"`) {
		t.Errorf("expected mutable prefix in userdata")
	}
	if !strings.Contains(raw, `prefix = "lock"`) {
		t.Errorf("expected lock prefix in userdata")
	}

	// Local store paths should NOT appear when S3 backend is used.
	if strings.Contains(raw, `path = "/opt/loreserver/store/immutable"`) {
		t.Errorf("local store paths should not appear for S3 backend")
	}

	// Store backend label in completion message.
	if !strings.Contains(raw, "store=s3") {
		t.Errorf("expected store=s3 in completion message")
	}
}

func TestGenerateRawLocalStoreBackend(t *testing.T) {
	raw, err := GenerateRaw(UserDataConfig{
		StorePath:    "/opt/loreserver/store",
		ConfigDir:    "/etc/loreserver",
		GRPCPort:     41337,
		HTTPPort:     41339,
		StoreBackend: StoreBackendLocal,
	})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}

	// Local store config should be present.
	if !strings.Contains(raw, `mode = "local"`) {
		t.Errorf("expected local store mode in userdata")
	}
	if !strings.Contains(raw, `path = "/opt/loreserver/store/immutable"`) {
		t.Errorf("expected local store path in userdata")
	}

	// S3 config should NOT appear for local backend.
	if strings.Contains(raw, `mode = "s3"`) {
		t.Errorf("s3 store mode should not appear for local backend")
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
		if cfg.StoreBackend != StoreBackendLocal {
			t.Errorf("StoreBackend = %q, want local", cfg.StoreBackend)
		}
	})
	t.Run("preserves existing values", func(t *testing.T) {
		cfg := UserDataConfig{
			StorePath:    "/custom/store",
			ConfigDir:    "/custom/config",
			GRPCPort:     9999,
			HTTPPort:     9998,
			StoreBackend: StoreBackendS3,
			StoreBucket:  "my-bucket",
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
		if cfg.StoreBackend != StoreBackendS3 {
			t.Errorf("StoreBackend = %q, want s3", cfg.StoreBackend)
		}
		if cfg.StoreBucket != "my-bucket" {
			t.Errorf("StoreBucket = %q, want my-bucket", cfg.StoreBucket)
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

func TestGenerateBase64S3Store(t *testing.T) {
	encoded, err := Generate(UserDataConfig{
		StoreBackend: StoreBackendS3,
		StoreBucket:  "test-bucket",
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("not valid base64: %v", err)
	}
	s := string(decoded)
	if !strings.Contains(s, `mode = "s3"`) {
		t.Error("decoded S3 userdata missing s3 mode")
	}
	if !strings.Contains(s, `bucket = "test-bucket"`) {
		t.Error("decoded S3 userdata missing bucket name")
	}
}
