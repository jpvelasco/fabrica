package credentials_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jpvelasco/fabrica/internal/credentials"
)

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
