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
