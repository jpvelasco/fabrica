package horde

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateRawContainsDockerCompose(t *testing.T) {
	cfg := UserDataConfig{MongoPassword: "testpass123", Port: 5000}
	got, err := GenerateRaw(cfg)
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	if !strings.Contains(got, "docker compose") {
		t.Error("docker compose not found in rendered script")
	}
	if !strings.Contains(got, "/etc/horde") {
		t.Error("/etc/horde path not found in rendered script")
	}
}

func TestGenerateRawRetriesComposeOnColdBoot(t *testing.T) {
	cfg := UserDataConfig{MongoPassword: "p", Port: 5000}
	got, err := GenerateRaw(cfg)
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	// Compose start is wrapped in a retry loop, not a bare single call:
	// the loop form carries "&& break" on the same line, so a standalone
	// "docker compose up -d\n" line must be absent.
	if strings.Contains(got, "docker compose up -d\n") {
		t.Error("expected a retry loop around docker compose up -d, not a bare single call")
	}
	if !strings.Contains(got, "docker compose up -d && break") {
		t.Error("retry loop form 'docker compose up -d && break' not found")
	}
	if !strings.Contains(got, "for i in $(seq 1 8)") {
		t.Error("compose retry loop (8 attempts) not found")
	}
	// Fail-closed on exhaustion: the loop's final attempt exits non-zero
	// with a clear log line.
	if !strings.Contains(got, "ERROR: docker compose up -d failed") {
		t.Error("fail-closed log line for compose retry exhaustion not found")
	}
	// The readiness sentinel is written only on the success path — both
	// fail-closed exits come before it, and it is the script's final line.
	sentinelIdx := strings.Index(got, "touch /var/lib/cloud/instance/horde-ready")
	if sentinelIdx < 0 {
		t.Fatal("readiness sentinel not found in rendered script")
	}
	for _, marker := range []string{
		"ERROR: docker compose up -d failed",
		"ERROR: Horde did not become ready within 5m",
	} {
		markerIdx := strings.Index(got, marker)
		if markerIdx < 0 {
			t.Fatalf("fail-closed exit line %q not found", marker)
		}
		if markerIdx > sentinelIdx {
			t.Errorf("fail-closed exit %q comes after the readiness sentinel; sentinel must be success-path only", marker)
		}
	}
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if lines[len(lines)-1] != "touch /var/lib/cloud/instance/horde-ready" {
		t.Errorf("readiness sentinel should be the script's final line, got %q", lines[len(lines)-1])
	}
}

func TestGenerateRawContainsPipefail(t *testing.T) {
	cfg := UserDataConfig{MongoPassword: "p", Port: 5000}
	got, err := GenerateRaw(cfg)
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	if !strings.Contains(got, "set -euo pipefail") {
		t.Error("set -euo pipefail not found")
	}
}

func TestGenerateRawContainsHordeReadySentinel(t *testing.T) {
	cfg := UserDataConfig{MongoPassword: "p", Port: 5000}
	got, err := GenerateRaw(cfg)
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	if !strings.Contains(got, "horde-ready") {
		t.Error("readiness sentinel not found in script")
	}
}

func TestGenerateRawContainsPorts(t *testing.T) {
	cfg := UserDataConfig{MongoPassword: "p", Port: 5001}
	got, err := GenerateRaw(cfg)
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	if !strings.Contains(got, "5001") {
		t.Error("HTTP port not found in rendered script")
	}
}

func TestGenerateRawEmptyPasswordErrors(t *testing.T) {
	_, err := GenerateRaw(UserDataConfig{Port: 5000})
	if err == nil {
		t.Fatal("expected error for empty MongoPassword")
	}
	if !strings.Contains(err.Error(), "MongoPassword") {
		t.Errorf("error %q should mention MongoPassword", err.Error())
	}
}

func TestGenerateRawPasswordStillValidated(t *testing.T) {
	secret := "do-not-embed-abc" // NOLINTALL // test-only sentinel, not a credential
	cfg := UserDataConfig{MongoPassword: secret, Port: 5000}
	got, err := GenerateRaw(cfg)
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	// Password is validated but not embedded in script (Docker compose handles it)
	if !strings.Contains(got, "#!/bin/bash") {
		t.Error("script shebang not found")
	}
	if strings.Contains(got, secret) {
		t.Error("MongoPassword must not be embedded in the rendered script")
	}
}

func TestGenerateReturnsBase64(t *testing.T) {
	cfg := UserDataConfig{MongoPassword: "p", Port: 5000}
	got, err := Generate(cfg)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		t.Fatalf("output is not valid base64: %v", err)
	}
	if !strings.Contains(string(decoded), "#!/bin/bash") {
		t.Error("decoded output does not contain #!/bin/bash")
	}
}

func TestGenerateEmptyPasswordErrors(t *testing.T) {
	_, err := Generate(UserDataConfig{Port: 5000})
	if err == nil {
		t.Fatal("expected error for empty MongoPassword")
	}
}

func TestValidate(t *testing.T) {
	t.Run("empty mongo password", func(t *testing.T) {
		cfg := UserDataConfig{}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected error for empty MongoPassword")
		}
		if !strings.Contains(err.Error(), "MongoPassword") {
			t.Errorf("error %q should mention MongoPassword", err.Error())
		}
	})
	t.Run("valid config", func(t *testing.T) {
		cfg := UserDataConfig{MongoPassword: "secret"}
		if err := cfg.validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
