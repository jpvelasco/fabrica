package perforce

import (
	"strings"
	"testing"
)

func TestGenerateRaw_LatestVersion(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "latest", AdminPass: "testpass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(got, "helix-p4d=") {
		t.Error("latest version should not pin a version in apt-get install")
	}
	if !strings.Contains(got, "apt-get install -y helix-p4d") {
		t.Error("missing 'apt-get install -y helix-p4d'")
	}
}

func TestGenerateRaw_PinnedVersion(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "2024.2", AdminPass: "testpass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, `helix-p4d=2024.2`) {
		t.Errorf("expected version pin 'helix-p4d=2024.2', got:\n%s", got)
	}
}

func TestGenerateRaw_PinnedVersionWithBuild(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "2024.2/2659294", AdminPass: "testpass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, `helix-p4d=2024.2/2659294`) {
		t.Errorf("expected version pin 'helix-p4d=2024.2/2659294', got:\n%s", got)
	}
}

func TestGenerateRaw_AdminPasswordAppearsOnce(t *testing.T) {
	pass := "s3cr3tP@ssw0rd"
	got, err := GenerateRaw(UserDataConfig{Version: "2024.2", AdminPass: pass})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	count := strings.Count(got, pass)
	if count != 1 {
		t.Errorf("admin password appears %d times, want exactly 1", count)
	}
}

func TestGenerateRaw_MountPoint(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "2024.2", AdminPass: "testpass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "/hxdepots") {
		t.Error("expected mount point '/hxdepots'")
	}
}

func TestGenerateRaw_PipefailPresent(t *testing.T) {
	got, err := GenerateRaw(UserDataConfig{Version: "2024.2", AdminPass: "testpass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "set -euo pipefail") {
		t.Error("expected 'set -euo pipefail'")
	}
}

func TestGenerateRaw_EmptyAdminPassError(t *testing.T) {
	_, err := GenerateRaw(UserDataConfig{Version: "2024.2", AdminPass: ""})
	if err == nil {
		t.Error("expected error for empty AdminPass")
	}
}

func TestGenerate_ReturnsBase64(t *testing.T) {
	got, err := Generate(UserDataConfig{Version: "2024.2", AdminPass: "testpass"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// base64 strings only contain A-Z, a-z, 0-9, +, /, =
	for _, c := range got {
		if !isBase64Char(c) {
			t.Errorf("Generate returned non-base64 character %q in output", c)
			break
		}
	}
}

func isBase64Char(c rune) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9') || c == '+' || c == '/' || c == '='
}
