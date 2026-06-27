package ami_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jpvelasco/fabrica/cmd/horde/ami"
)

// buildTestRoot wires the ami command under a minimal root that owns the
// persistent --dry-run flag, mirroring the real flag hierarchy (dry-run lives
// on the root command, not on "ami build").
func buildTestRoot(out *bytes.Buffer) *cobra.Command {
	root := &cobra.Command{Use: "fabrica", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Bool("dry-run", false, "")
	root.AddCommand(ami.New(out))
	return root
}

func execAmi(t *testing.T, out *bytes.Buffer, args ...string) error {
	t.Helper()
	root := buildTestRoot(out)
	root.SetArgs(args)
	root.SetOut(out)
	root.SetErr(out)
	return root.ExecuteContext(context.Background())
}

func TestAmiBuild_WritesFiles(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer

	err := execAmi(t, &out, "ami", "build", "--output-dir", dir, "--horde-version", "5.5.0")
	if err != nil {
		t.Fatalf("execute returned error: %v", err)
	}

	for _, name := range []string{"image-builder-recipe.json", "component.yaml", "build-guide.md"} {
		if _, statErr := os.Stat(filepath.Join(dir, name)); statErr != nil {
			t.Errorf("expected %s to be written: %v", name, statErr)
		}
	}
	// Packer template should NOT exist without --include-packer.
	if _, statErr := os.Stat(filepath.Join(dir, "packer.pkr.hcl")); statErr == nil {
		t.Error("packer.pkr.hcl written without --include-packer")
	}
}

func TestAmiBuild_IncludePacker(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer

	if err := execAmi(t, &out, "ami", "build", "--output-dir", dir, "--include-packer"); err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "packer.pkr.hcl")); statErr != nil {
		t.Errorf("expected packer.pkr.hcl with --include-packer: %v", statErr)
	}
}

func TestAmiBuild_DryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer

	if err := execAmi(t, &out, "ami", "build", "--output-dir", dir, "--dry-run"); err != nil {
		t.Fatalf("execute returned error: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading output dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("dry run wrote %d files, expected 0", len(entries))
	}
	if !strings.Contains(out.String(), "Dry run") {
		t.Errorf("expected dry-run notice in output, got:\n%s", out.String())
	}
}

func TestAmiBuild_InvalidFlagValue(t *testing.T) {
	var out bytes.Buffer
	err := execAmi(t, &out, "ami", "build", "--output-dir", t.TempDir(), "--install", "podman")
	if err == nil {
		t.Fatal("expected error for invalid --install value, got nil")
	}
	if !strings.Contains(err.Error(), "--install must be") {
		t.Errorf("unexpected error: %v", err)
	}
}
