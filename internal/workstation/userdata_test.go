package workstation

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/assert"
)

func TestGenerateRawRequiresSessionPassword(t *testing.T) {
	_, err := GenerateRaw(UserDataConfig{})
	if err == nil {
		t.Fatal("expected error when SessionPassword is empty")
	}
	assert.Contains(t, err.Error(), "SessionPassword")
}

func TestGenerateRawRequiresDCVOnAMI(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{SessionPassword: "hunter2"})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	for _, want := range []string{
		"command -v dcv",
		"NICE DCV is not installed on this AMI",
		"hunter2",
		"systemctl enable dcvserver",
		"systemctl restart dcvserver",
	} {
		assert.Contains(t, got, want)
	}
	if strings.Contains(got, "snap install") || strings.Contains(got, "apt-get install -y dcv-server") {
		t.Error("userdata must not install DCV at boot; the AMI is DCV-first")
	}
	if strings.Contains(got, "|| true") {
		t.Error("userdata must not soft-fail DCV enable")
	}
}

func TestGenerateRawUsesCurrentDCVCLI(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{SessionPassword: "hunter2"})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	// DCV 2025.0.x removed configure-session/configure; the script must not
	// call them (under set -euo pipefail that aborts before session creation).
	for _, gone := range []string{"configure-session", "dcv configure "} {
		if strings.Contains(got, gone) {
			t.Errorf("userdata must not call removed DCV CLI command %q: #438", gone)
		}
	}
	for _, want := range []string{
		"dcv set-config --section connectivity --key idle-timeout",
		"hunter2",
		"chpasswd",
		"dcv list-sessions",
		"ERROR: DCV session 'workstation' did not appear",
	} {
		assert.Contains(t, got, want)
	}
	// create-session spans two lines with a backslash continuation; join the
	// continuation and assert the full command line.
	joined := strings.ReplaceAll(got, `
`+"\n", " ")
	assert.Contains(t, joined, "dcv create-session --type virtual --user \"$DCV_USER\" --owner \"$DCV_USER\"")
	assert.Contains(t, joined, "--storage-root /home/\"$DCV_USER\"/dcv workstation")
}

func TestGenerateRawIdleTimeout(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{
		SessionPassword:    "pw",
		IdleTimeoutMinutes: 30,
	})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	assert.Contains(t, got, "30")
}

func TestGenerateProducesValidBase64(t *testing.T) {
	b64, err := Generate(UserDataConfig{SessionPassword: "pw"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if len(decoded) == 0 {
		t.Error("decoded userdata is empty")
	}
}

func TestGenerateRawDefaultIdleTimeout(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{SessionPassword: "pw"})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	assert.Contains(t, got, "60")
}

func TestGenerateRawMountPerforceRequiresAddr(t *testing.T) {
	_, err := GenerateRaw(UserDataConfig{
		SessionPassword: "pw",
		MountPerforce:   true,
		// PerforceServerAddr intentionally empty
	})
	if err == nil {
		t.Fatal("expected error when MountPerforce=true and PerforceServerAddr is empty")
	}
	assert.Contains(t, err.Error(), "PerforceServerAddr")
}

func TestGenerateRawMountPerforceInjectsP4Config(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{
		SessionPassword:    "pw",
		MountPerforce:      true,
		PerforceServerAddr: "10.0.1.5:1666",
	})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	for _, want := range []string{
		"helix-cli",
		"p4config",
		"10.0.1.5:1666",
		"P4PORT",
	} {
		assert.Contains(t, got, want)
	}
}

func TestGenerateRawNoMountPerforceNoP4Block(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{SessionPassword: "pw"})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	if strings.Contains(got, "helix-cli") {
		t.Error("without --mount-perforce, userdata must not contain helix-cli")
	}
}

func TestApplyDefaults(t *testing.T) {
	t.Run("fills zero idle timeout", func(t *testing.T) {
		cfg := UserDataConfig{SessionPassword: "pw"}
		cfg.applyDefaults()
		if cfg.IdleTimeoutMinutes != DefaultIdleTimeoutMinutes {
			t.Errorf("IdleTimeoutMinutes = %d, want %d", cfg.IdleTimeoutMinutes, DefaultIdleTimeoutMinutes)
		}
	})
	t.Run("preserves existing idle timeout", func(t *testing.T) {
		cfg := UserDataConfig{SessionPassword: "pw", IdleTimeoutMinutes: 45}
		cfg.applyDefaults()
		if cfg.IdleTimeoutMinutes != 45 {
			t.Errorf("IdleTimeoutMinutes = %d, want 45", cfg.IdleTimeoutMinutes)
		}
	})
}

func TestValidate(t *testing.T) {
	t.Run("empty session password", func(t *testing.T) {
		cfg := UserDataConfig{}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected error for empty SessionPassword")
		}
		assert.Contains(t, err.Error(), "SessionPassword")
	})
	t.Run("mount perforce without addr", func(t *testing.T) {
		cfg := UserDataConfig{SessionPassword: "pw", MountPerforce: true}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected error for MountPerforce without PerforceServerAddr")
		}
		assert.Contains(t, err.Error(), "PerforceServerAddr")
	})
	t.Run("valid config", func(t *testing.T) {
		cfg := UserDataConfig{SessionPassword: "pw"}
		if err := cfg.validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("valid config with perforce", func(t *testing.T) {
		cfg := UserDataConfig{SessionPassword: "pw", MountPerforce: true, PerforceServerAddr: "10.0.1.5:1666"}
		if err := cfg.validate(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestGenerate_ValidationError(t *testing.T) {
	_, err := Generate(UserDataConfig{})
	if err == nil {
		t.Fatal("expected error for empty SessionPassword")
	}
}
