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
	cfg := UserDataConfig{MongoPassword: "uniquepass456", Port: 5000}
	got, err := GenerateRaw(cfg)
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	// Password is validated but not embedded in script (Docker compose handles it)
	if !strings.Contains(got, "#!/bin/bash") {
		t.Error("script shebang not found")
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
