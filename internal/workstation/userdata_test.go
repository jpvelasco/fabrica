package workstation

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestGenerateRawRequiresSessionPassword(t *testing.T) {
	_, err := GenerateRaw(UserDataConfig{})
	if err == nil {
		t.Fatal("expected error when SessionPassword is empty")
	}
	if !containsStr(err.Error(), "SessionPassword") {
		t.Errorf("error %q should mention SessionPassword", err.Error())
	}
}

func TestGenerateRawContainsDCVInstall(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{SessionPassword: "hunter2"})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	for _, want := range []string{
		"dcv",
		"hunter2",
	} {
		if !containsStr(strings.ToLower(got), strings.ToLower(want)) {
			t.Errorf("userdata does not contain %q", want)
		}
	}
}

func TestGenerateRawIdleTimeout(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{
		SessionPassword:    "pw",
		IdleTimeoutMinutes: 30,
	})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	if !containsStr(got, "30") {
		t.Error("idle timeout 30 should appear in userdata")
	}
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
	if !containsStr(got, "60") {
		t.Error("default idle timeout 60 should appear in userdata")
	}
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
	if !containsStr(err.Error(), "PerforceServerAddr") {
		t.Errorf("error %q should mention PerforceServerAddr", err.Error())
	}
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
		if !containsStr(got, want) {
			t.Errorf("mount-perforce userdata does not contain %q", want)
		}
	}
}

func TestGenerateRawNoMountPerforceNoP4Block(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{SessionPassword: "pw"})
	if err != nil {
		t.Fatalf("GenerateRaw: %v", err)
	}
	if containsStr(got, "helix-cli") {
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
		if !containsStr(err.Error(), "SessionPassword") {
			t.Errorf("error %q should mention SessionPassword", err.Error())
		}
	})
	t.Run("mount perforce without addr", func(t *testing.T) {
		cfg := UserDataConfig{SessionPassword: "pw", MountPerforce: true}
		err := cfg.validate()
		if err == nil {
			t.Fatal("expected error for MountPerforce without PerforceServerAddr")
		}
		if !containsStr(err.Error(), "PerforceServerAddr") {
			t.Errorf("error %q should mention PerforceServerAddr", err.Error())
		}
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
