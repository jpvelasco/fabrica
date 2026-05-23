package credentials_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/credentials"
)

func TestGeneratePassword_Length(t *testing.T) {
	for _, length := range []int{8, 16, 24, 32} {
		p, err := credentials.GeneratePassword(length)
		if err != nil {
			t.Fatalf("length %d: unexpected error: %v", length, err)
		}
		if len(p) != length {
			t.Errorf("length %d: got %d chars", length, len(p))
		}
	}
}

func TestGeneratePassword_Charset(t *testing.T) {
	p, err := credentials.GeneratePassword(100)
	if err != nil {
		t.Fatal(err)
	}
	for _, ch := range p {
		if !strings.ContainsRune(credentials.PasswordChars, ch) {
			t.Errorf("unexpected character %q in password", ch)
		}
	}
}

func TestGeneratePassword_Unique(t *testing.T) {
	a, _ := credentials.GeneratePassword(24)
	b, _ := credentials.GeneratePassword(24)
	if a == b {
		t.Error("two generated passwords were identical (extremely unlikely)")
	}
}

func TestWriteCredentials_CreatesFileAndDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "creds.yaml")
	content := "key: value\n"

	if err := credentials.WriteCredentials(path, content); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read written file: %v", err)
	}
	if string(got) != content {
		t.Errorf("content mismatch: got %q want %q", got, content)
	}
}

func TestFormatPerforce(t *testing.T) {
	out := credentials.FormatPerforce("mypass")
	if !strings.Contains(out, "admin_password:") {
		t.Error("FormatPerforce output missing admin_password key")
	}
	if !strings.Contains(out, "mypass") {
		t.Error("FormatPerforce output missing password value")
	}
}

func TestFormatHorde(t *testing.T) {
	out := credentials.FormatHorde("mongopass")
	if !strings.Contains(out, "mongodb_password:") {
		t.Error("FormatHorde output missing mongodb_password key")
	}
	if !strings.Contains(out, "horde_service_token:") {
		t.Error("FormatHorde output missing horde_service_token key")
	}
	if !strings.Contains(out, "mongopass") {
		t.Error("FormatHorde output missing password value")
	}
}
