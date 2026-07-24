package ddc

import (
	"strings"
	"testing"
)

func TestGenerateRawNoRemotePeers(t *testing.T) {
	raw, err := GenerateRaw(UserDataConfig{
		Bucket: "b", Region: "us-east-1", Namespace: "ns",
		PublicPort: 80, InternalPort: 8080, Backend: BackendZen,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Must not configure remote peers or multi-region replication blocks.
	if strings.Contains(raw, "Replicators:") || strings.Contains(raw, "ConnectionString:") {
		t.Fatalf("unexpected multi-region content: %s", raw)
	}
	if !strings.Contains(raw, "FABRICA_DDC_BUCKET=$BUCKET") {
		t.Fatalf("missing bucket env: %s", raw)
	}
	if !strings.Contains(raw, `BUCKET="b"`) {
		t.Fatalf("missing bucket assignment: %s", raw)
	}
	if !strings.Contains(raw, "Single-region V1") {
		t.Fatalf("missing single-region note")
	}
}

func TestGenerateBase64(t *testing.T) {
	b64, err := Generate(UserDataConfig{
		Bucket: "b", Region: "r", Namespace: "n", PublicPort: 80, InternalPort: 8080, Backend: BackendZen,
	})
	if err != nil || b64 == "" {
		t.Fatalf("Generate: %v %q", err, b64)
	}
}

func TestGenerateScyllaRaw(t *testing.T) {
	raw, err := GenerateScyllaRaw(ScyllaUserDataConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "NOT production HA") {
		t.Fatalf("%s", raw)
	}
	b64, err := GenerateScylla(ScyllaUserDataConfig{ClusterName: "c"})
	if err != nil || b64 == "" {
		t.Fatalf("GenerateScylla: %v", err)
	}
}

func TestApplyDefaults(t *testing.T) {
	t.Run("empty config fills store path", func(t *testing.T) {
		cfg := UserDataConfig{Bucket: "b", Region: "r", Namespace: "n"}
		cfg.applyDefaults()
		if cfg.StorePath != DefaultStorePath {
			t.Errorf("StorePath = %q, want %q", cfg.StorePath, DefaultStorePath)
		}
	})
	t.Run("preserves existing store path", func(t *testing.T) {
		cfg := UserDataConfig{StorePath: "/custom", Bucket: "b", Region: "r", Namespace: "n"}
		cfg.applyDefaults()
		if cfg.StorePath != "/custom" {
			t.Errorf("StorePath = %q, want /custom", cfg.StorePath)
		}
	})
}

func TestScyllaApplyDefaults(t *testing.T) {
	t.Run("fills all zeros", func(t *testing.T) {
		cfg := ScyllaUserDataConfig{}
		cfg.applyDefaults()
		if cfg.StorePath != "/var/lib/scylla" {
			t.Errorf("StorePath = %q, want /var/lib/scylla", cfg.StorePath)
		}
		if cfg.ClusterName != "fabrica-ddc" {
			t.Errorf("ClusterName = %q, want fabrica-ddc", cfg.ClusterName)
		}
	})
	t.Run("preserves existing values", func(t *testing.T) {
		cfg := ScyllaUserDataConfig{StorePath: "/custom", ClusterName: "mycluster"}
		cfg.applyDefaults()
		if cfg.StorePath != "/custom" {
			t.Errorf("StorePath = %q, want /custom", cfg.StorePath)
		}
		if cfg.ClusterName != "mycluster" {
			t.Errorf("ClusterName = %q, want mycluster", cfg.ClusterName)
		}
	})
}
