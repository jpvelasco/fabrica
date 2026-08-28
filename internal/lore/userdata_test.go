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
		StoreTables: []string{
			"my-lore-bucket-fragments",
			"my-lore-bucket-metadata",
			"my-lore-bucket-mutable",
			"my-lore-bucket-locks",
		},
	})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}

	// AWS store plugin config should be present (mode = "aws" per
	// config-aws.toml). The legacy mode = "s3" workaround must be gone.
	if !strings.Contains(raw, `mode = "aws"`) {
		t.Errorf("expected aws store mode in userdata")
	}
	if strings.Contains(raw, `mode = "s3"`) {
		t.Errorf("legacy s3 store mode must not appear in userdata")
	}
	if !strings.Contains(raw, `s3_bucket = "my-lore-bucket"`) {
		t.Errorf("expected s3_bucket name in userdata")
	}
	for _, want := range []string{
		"dynamodb_fragments_table = \"my-lore-bucket-fragments\"",
		"dynamodb_metadata_table = \"my-lore-bucket-metadata\"",
		"dynamodb_table = \"my-lore-bucket-mutable\"",
		"dynamodb_table = \"my-lore-bucket-locks\"",
		"[plugins.aws.immutable_store]",
		"[plugins.aws.mutable_store]",
		"[plugins.aws.lock_store]",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("userdata missing %q", want)
		}
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

func TestGenerateRawRemainsCompatibleWithDefaultAMIContract(t *testing.T) {
	contract := DefaultAMIContract()
	tests := []struct {
		name         string
		storeBackend string
		storeBucket  string
		wantStore    string
	}{
		{
			name:         "local store",
			storeBackend: StoreBackendLocal,
			wantStore:    "mode = \"local\"",
		},
		{
			name:         "S3 store",
			storeBackend: StoreBackendS3,
			storeBucket:  "fabrica-lore-store-test",
			wantStore:    "mode = \"aws\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := GenerateRaw(UserDataConfig{
				StoreBackend: tt.storeBackend,
				StoreBucket:  tt.storeBucket,
			})
			if err != nil {
				t.Fatalf("GenerateRaw() error: %v", err)
			}
			for _, want := range []string{
				"CONFIG_DIR=\"" + contract.ConfigDir + "\"",
				"$CONFIG_DIR/local.toml",
				"systemctl enable " + contract.ServiceName,
				"systemctl restart " + contract.ServiceName,
				tt.wantStore,
			} {
				if !strings.Contains(raw, want) {
					t.Errorf("cloud-init missing AMI contract behavior %q", want)
				}
			}
		})
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
	if !strings.Contains(s, `mode = "aws"`) {
		t.Error("decoded S3 userdata missing aws mode")
	}
	if !strings.Contains(s, `s3_bucket = "test-bucket"`) {
		t.Error("decoded S3 userdata missing s3_bucket name")
	}
}
