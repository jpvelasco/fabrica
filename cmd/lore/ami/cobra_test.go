package ami_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/lore/ami"
	"github.com/spf13/cobra"
)

func setupCmd(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	out := &bytes.Buffer{}
	root := &cobra.Command{Use: "fabrica"}
	root.PersistentFlags().Bool("dry-run", false, "")
	root.AddCommand(ami.New(out))
	return root, out
}

func TestAMIBuildDryRun(t *testing.T) {
	root, out := setupCmd(t)
	root.SetArgs([]string{"--dry-run", "ami", "build"})
	if err := root.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error: %v", err)
	}
	got := out.String()
	if !bytes.Contains([]byte(got), []byte("Would generate")) {
		t.Errorf("expected 'Would generate', got:\n%s", got)
	}
	if !bytes.Contains([]byte(got), []byte("Dry run")) {
		t.Errorf("expected 'Dry run', got:\n%s", got)
	}
}

func TestAMIBuildDefaults(t *testing.T) {
	root, out := setupCmd(t)
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "lore-ami")
	root.SetArgs([]string{"ami", "build", "--output-dir", outputDir})
	if err := root.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error: %v", err)
	}

	got := out.String()
	if !bytes.Contains([]byte(got), []byte("Generated 6 files")) {
		t.Errorf("expected 'Generated 6 files', got:\n%s", got)
	}

	// Check files were created
	expectedFiles := []string{
		"image-builder-recipe.json",
		"component.yaml",
		"install-lore.sh",
		"verify-lore-ami-bake.sh",
		"verify-lore-ami-runtime.sh",
		"build-guide.md",
	}
	for _, f := range expectedFiles {
		path := filepath.Join(outputDir, f)
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected file %s to exist: %v", path, err)
		}
	}
}

func TestAMIBuildWithPacker(t *testing.T) {
	root, out := setupCmd(t)
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "lore-ami")
	root.SetArgs([]string{"ami", "build", "--output-dir", outputDir, "--include-packer"})
	if err := root.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error: %v", err)
	}

	got := out.String()
	if !bytes.Contains([]byte(got), []byte("Generated 7 files")) {
		t.Errorf("expected 'Generated 7 files', got:\n%s", got)
	}

	path := filepath.Join(outputDir, "packer.pkr.hcl")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("expected file %s to exist: %v", path, err)
	}
}

func TestAMIBuildInvalidVersion(t *testing.T) {
	root, _ := setupCmd(t)
	root.SetArgs([]string{"ami", "build", "--lore-version", "bad"})
	err := root.ExecuteContext(t.Context())
	if err == nil {
		t.Error("expected error for invalid version, got nil")
	}
}

func TestAMIBuildCustomFlags(t *testing.T) {
	root, out := setupCmd(t)
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "lore-ami")
	root.SetArgs([]string{
		"ami", "build",
		"--lore-version", "5.4.0",
		"--region", "eu-west-1",
		"--output-dir", outputDir,
		"--name", "my-lore-ami",
	})
	if err := root.ExecuteContext(t.Context()); err != nil {
		t.Fatalf("ExecuteContext() error: %v", err)
	}

	got := out.String()
	if !bytes.Contains([]byte(got), []byte("my-lore-ami")) {
		t.Errorf("expected custom name in output, got:\n%s", got)
	}
	if !bytes.Contains([]byte(got), []byte("5.4.0")) {
		t.Errorf("expected custom version in output, got:\n%s", got)
	}
}
