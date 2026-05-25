package ami

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildCommandRun_Docker(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	written := map[string][]byte{}

	bc := buildCommand{
		out: &buf,
		cfg: BuildConfig{
			Version:       "5.5.0",
			Install:       "docker",
			BaseImage:     "ami-test123",
			Name:          "test-horde",
			OutputDir:     dir,
			IncludePacker: false,
		},
		writeFile: func(path string, data []byte, perm os.FileMode) error {
			written[filepath.Base(path)] = data
			return nil
		},
	}

	if err := bc.run(); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	if _, ok := written["image-builder-recipe.json"]; !ok {
		t.Error("expected image-builder-recipe.json to be written")
	}
	if _, ok := written["build-guide.md"]; !ok {
		t.Error("expected build-guide.md to be written")
	}
	if _, ok := written["packer.pkr.hcl"]; ok {
		t.Error("packer.pkr.hcl should not be written without --include-packer")
	}

	recipe := string(written["image-builder-recipe.json"])
	if !strings.Contains(recipe, "ami-test123") {
		t.Error("recipe should contain base image AMI ID")
	}
	if !strings.Contains(recipe, "5.5.0") {
		t.Error("recipe should contain Horde version")
	}
	if !strings.Contains(recipe, "docker") {
		t.Error("recipe should reference docker install method")
	}

	guide := string(written["build-guide.md"])
	if !strings.Contains(guide, "Docker") {
		t.Error("build guide should contain Docker instructions for docker install")
	}
}

func TestBuildCommandRun_Native(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	written := map[string][]byte{}

	bc := buildCommand{
		out: &buf,
		cfg: BuildConfig{
			Version:       "5.4.0",
			Install:       "native",
			BaseImage:     "ami-native456",
			Name:          "test-horde-native",
			OutputDir:     dir,
			IncludePacker: false,
		},
		writeFile: func(path string, data []byte, perm os.FileMode) error {
			written[filepath.Base(path)] = data
			return nil
		},
	}

	if err := bc.run(); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	guide := string(written["build-guide.md"])
	if !strings.Contains(guide, "Native Install") {
		t.Error("build guide should contain Native Install section")
	}
	if strings.Contains(guide, "Docker CE") {
		t.Error("build guide should not mention Docker CE for native install")
	}
}

func TestBuildCommandRun_IncludePacker(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	written := map[string][]byte{}

	bc := buildCommand{
		out: &buf,
		cfg: BuildConfig{
			Version:       "5.5.0",
			Install:       "docker",
			BaseImage:     "ami-test123",
			Name:          "test-horde",
			OutputDir:     dir,
			IncludePacker: true,
		},
		writeFile: func(path string, data []byte, perm os.FileMode) error {
			written[filepath.Base(path)] = data
			return nil
		},
	}

	if err := bc.run(); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	if _, ok := written["packer.pkr.hcl"]; !ok {
		t.Error("expected packer.pkr.hcl to be written with --include-packer")
	}

	packer := string(written["packer.pkr.hcl"])
	if !strings.Contains(packer, "ami-test123") {
		t.Error("packer template should contain base image AMI ID")
	}
	if !strings.Contains(packer, "5.5.0") {
		t.Error("packer template should contain Horde version")
	}
}

func TestBuildCommandRun_InvalidInstall(t *testing.T) {
	var buf bytes.Buffer
	bc := buildCommand{
		out: &buf,
		cfg: BuildConfig{
			Version: "5.5.0",
			Install: "invalid",
		},
		writeFile: func(path string, data []byte, perm os.FileMode) error { return nil },
	}

	err := bc.run()
	if err == nil {
		t.Fatal("expected error for invalid install method")
	}
	if !strings.Contains(err.Error(), "--install must be") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestBuildCommandRun_DefaultName(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	written := map[string][]byte{}

	bc := buildCommand{
		out: &buf,
		cfg: BuildConfig{
			Version:   "5.5.0",
			Install:   "docker",
			BaseImage: "ami-test",
			Name:      "",
			OutputDir: dir,
		},
		writeFile: func(path string, data []byte, perm os.FileMode) error {
			written[filepath.Base(path)] = data
			return nil
		},
	}

	if err := bc.run(); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	recipe := string(written["image-builder-recipe.json"])
	if !strings.Contains(recipe, "fabrica-horde-5.5.0") {
		t.Error("default name should be fabrica-horde-<version>")
	}
}

func TestBuildCommandRun_WriteError(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer

	bc := buildCommand{
		out: &buf,
		cfg: BuildConfig{
			Version:   "5.5.0",
			Install:   "docker",
			BaseImage: "ami-test",
			Name:      "test",
			OutputDir: dir,
		},
		writeFile: func(path string, data []byte, perm os.FileMode) error {
			return fmt.Errorf("disk full")
		},
	}

	err := bc.run()
	if err == nil {
		t.Fatal("expected error when writeFile fails")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("error should propagate write failure: %v", err)
	}
}

func TestValidateImageBuilderJSON(t *testing.T) {
	valid := []byte(`{
		"name": "test",
		"version": "1.0.0",
		"parentImage": "ami-123",
		"blockDeviceMappings": [],
		"components": [{"componentArn": "arn:aws:imagebuilder:us-east-1:aws:component/foo"}]
	}`)
	if err := validateImageBuilderJSON(valid); err != nil {
		t.Errorf("unexpected error on valid JSON: %v", err)
	}

	missingName := []byte(`{"version": "1.0.0", "parentImage": "ami-123", "components": [{"componentArn": "x"}]}`)
	if err := validateImageBuilderJSON(missingName); err == nil {
		t.Error("expected error when name is missing")
	}

	missingParent := []byte(`{"name": "x", "version": "1.0.0", "components": [{"componentArn": "x"}]}`)
	if err := validateImageBuilderJSON(missingParent); err == nil {
		t.Error("expected error when parentImage is missing")
	}

	noComponents := []byte(`{"name": "x", "version": "1.0.0", "parentImage": "ami-123", "components": []}`)
	if err := validateImageBuilderJSON(noComponents); err == nil {
		t.Error("expected error when components is empty")
	}

	invalid := []byte(`not json`)
	if err := validateImageBuilderJSON(invalid); err == nil {
		t.Error("expected error for invalid JSON")
	}
}
