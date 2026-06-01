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
