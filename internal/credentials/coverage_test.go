package credentials_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jpvelasco/fabrica/internal/credentials"
)

func TestParsePerforceAdminPassword_SkipNonKeyAndEmpty(t *testing.T) {
	content := "# comment\n\nother: x\nadmin_password: \"\"\n"
	if _, err := credentials.ParsePerforceAdminPassword(content); err == nil {
		t.Fatal("expected empty password error")
	}
	content2 := "admin_password: 'single'\n"
	got, err := credentials.ParsePerforceAdminPassword(content2)
	if err != nil || got != "single" {
		t.Fatalf("got %q err=%v", got, err)
	}
	// line without admin_password key is skipped until missing
	if _, err := credentials.ParsePerforceAdminPassword("foo: bar\n"); err == nil {
		t.Fatal("expected not found")
	}
}

func TestWriteCredentials_InvalidDir(t *testing.T) {
	// Create a file where a directory is needed so MkdirAll fails when
	// parent path component is a file.
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "nested", "creds.yaml")
	if err := credentials.WriteCredentials(path, "x"); err == nil {
		t.Fatal("expected mkdir error")
	}
}
