package ami

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/lore"
)

func TestBuildValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     BuildConfig
		wantErr string
	}{
		{
			name:    "empty version",
			cfg:     BuildConfig{OutputDir: "out"},
			wantErr: "--lore-version is required",
		},
		{
			name:    "invalid version format",
			cfg:     BuildConfig{Version: "abc", OutputDir: "out"},
			wantErr: "--lore-version must be in the format X.Y or X.Y.Z",
		},
		{
			name:    "invalid ami",
			cfg:     BuildConfig{Version: "5.5.0", BaseImage: "not-an-ami", OutputDir: "out"},
			wantErr: "--base-image must be a valid AMI ID",
		},
		{
			name:    "invalid region",
			cfg:     BuildConfig{Version: "5.5.0", BaseImage: defaultBaseImage, Region: "not-a-region", OutputDir: "out"},
			wantErr: "--region must be a valid AWS region",
		},
		{
			name:    "invalid name characters",
			cfg:     BuildConfig{Version: "5.5.0", BaseImage: defaultBaseImage, Region: defaultRegion, Name: "bad name!", OutputDir: "out"},
			wantErr: "--name can only contain letters, numbers, dots, underscores, and hyphens",
		},
		{
			name: "name too long",
			cfg: BuildConfig{
				Version:   "5.5.0",
				BaseImage: defaultBaseImage,
				Region:    defaultRegion,
				Name:      strings.Repeat("a", 200),
				OutputDir: "out",
			},
			wantErr: fmt.Sprintf("--name must be %d characters or fewer", maxNameLength),
		},
		{
			name:    "empty output dir",
			cfg:     BuildConfig{Version: "5.5.0", BaseImage: defaultBaseImage, Region: defaultRegion},
			wantErr: "--output-dir is required",
		},
		{
			name: "valid defaults",
			cfg: BuildConfig{
				Version:   "5.5.0",
				BaseImage: defaultBaseImage,
				Region:    defaultRegion,
				OutputDir: "out",
			},
			wantErr: "",
		},
		{
			name: "valid latest",
			cfg: BuildConfig{
				Version:   "latest",
				BaseImage: defaultBaseImage,
				Region:    defaultRegion,
				OutputDir: "out",
			},
			wantErr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &buildCommand{cfg: tt.cfg}
			err := b.validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("validate() unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Errorf("validate() expected error containing %q, got nil", tt.wantErr)
				return
			}
			if !bytes.Contains([]byte(err.Error()), []byte(tt.wantErr)) {
				t.Errorf("validate() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestBuildDryRun(t *testing.T) {
	var out bytes.Buffer
	b := &buildCommand{
		out: &out,
		cfg: BuildConfig{
			Version:   "5.5.0",
			BaseImage: defaultBaseImage,
			Region:    defaultRegion,
			Name:      "fabrica-lore-5.5.0",
			OutputDir: "lore-ami",
			DryRun:    true,
		},
	}

	plannedFiles := []string{
		"image-builder-recipe.json",
		"component.yaml",
		"install-lore.sh",
		"verify-lore-ami-bake.sh",
		"verify-lore-ami-runtime.sh",
		"build-guide.md",
	}
	b.printHeader(plannedFiles)
	fmt.Fprintln(b.out, "Dry run — no files written.")
	fmt.Fprintln(b.out)
	b.printNextSteps()

	got := out.String()
	if !bytes.Contains([]byte(got), []byte("Would generate")) {
		t.Errorf("expected 'Would generate' in output, got:\n%s", got)
	}
	if !bytes.Contains([]byte(got), []byte("Dry run")) {
		t.Errorf("expected 'Dry run' in output, got:\n%s", got)
	}
	if !bytes.Contains([]byte(got), []byte("5.5.0")) {
		t.Errorf("expected version '5.5.0' in output, got:\n%s", got)
	}
	if !bytes.Contains([]byte(got), []byte("Would write 6 files")) {
		t.Errorf("expected shared contract artifacts in output, got:\n%s", got)
	}
}

func TestBuildPrintSuccess(t *testing.T) {
	var out bytes.Buffer
	b := &buildCommand{out: &out}
	files := []string{"image-builder-recipe.json", "component.yaml", "build-guide.md"}
	b.printSuccess(files)

	got := out.String()
	if !bytes.Contains([]byte(got), []byte("Generated 3 files")) {
		t.Errorf("expected 'Generated 3 files' in output, got:\n%s", got)
	}
}

func TestPlanVerb(t *testing.T) {
	if got := planVerb(true); got != "Would write" {
		t.Errorf("planVerb(true) = %q, want %q", got, "Would write")
	}
	if got := planVerb(false); got != "Writing" {
		t.Errorf("planVerb(false) = %q, want %q", got, "Writing")
	}
}

func TestBuildRunMkdirError(t *testing.T) {
	var out bytes.Buffer
	b := &buildCommand{
		out: &out,
		cfg: BuildConfig{
			Version:   "5.5.0",
			BaseImage: defaultBaseImage,
			Region:    defaultRegion,
			Name:      "fabrica-lore-5.5.0",
			OutputDir: "/nonexistent/impossible/path",
		},
		mkdirAll: func(path string, perm os.FileMode) error {
			return fmt.Errorf("permission denied")
		},
	}
	err := b.run()
	if err == nil {
		t.Error("expected error for mkdirAll failure, got nil")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("creating --output-dir")) {
		t.Errorf("expected 'creating --output-dir' in error, got: %v", err)
	}
}

func TestBuildRunWriteError(t *testing.T) {
	var out bytes.Buffer
	b := &buildCommand{
		out: &out,
		cfg: BuildConfig{
			Version:   "5.5.0",
			BaseImage: defaultBaseImage,
			Region:    defaultRegion,
			Name:      "fabrica-lore-5.5.0",
			OutputDir: "out",
		},
		mkdirAll: func(path string, perm os.FileMode) error { return nil },
		writeFile: func(path string, data []byte, perm os.FileMode) error {
			return fmt.Errorf("disk full")
		},
	}
	err := b.run()
	if err == nil {
		t.Error("expected error for writeFile failure, got nil")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("writing")) {
		t.Errorf("expected 'writing' in error, got: %v", err)
	}
}

func TestBuildRunWriteErrorPerFile(t *testing.T) {
	targets := []string{
		"image-builder-recipe.json",
		"component.yaml",
		"install-lore.sh",
		"verify-lore-ami-bake.sh",
		"verify-lore-ami-runtime.sh",
		"packer.pkr.hcl",
		"build-guide.md",
	}

	for _, target := range targets {
		t.Run(target, func(t *testing.T) {
			var out bytes.Buffer
			b := &buildCommand{
				out: &out,
				cfg: BuildConfig{
					Version:       "5.5.0",
					BaseImage:     defaultBaseImage,
					Region:        defaultRegion,
					Name:          "fabrica-lore-5.5.0",
					OutputDir:     "out",
					IncludePacker: true,
				},
				mkdirAll: func(path string, perm os.FileMode) error { return nil },
				writeFile: func(path string, data []byte, perm os.FileMode) error {
					if filepath.Base(path) == target {
						return fmt.Errorf("disk full")
					}
					return nil
				},
			}

			err := b.run()
			if err == nil {
				t.Fatalf("expected write failure for %s, got nil", target)
			}
			if !bytes.Contains([]byte(err.Error()), []byte(target)) {
				t.Errorf("error %q does not mention failed file %s", err, target)
			}
		})
	}
}

func TestBuildRunContractError(t *testing.T) {
	contract := lore.DefaultAMIContract()
	contract.ServiceName = ""

	b := &buildCommand{
		out: &bytes.Buffer{},
		cfg: BuildConfig{
			Version:   "5.5.0",
			BaseImage: defaultBaseImage,
			Region:    defaultRegion,
			OutputDir: "out",
		},
		contract: func() lore.AMIContract { return contract },
	}

	err := b.run()
	if err == nil {
		t.Fatal("expected contract error, got nil")
	}
	want := "generating Lore AMI install script"
	if !bytes.Contains([]byte(err.Error()), []byte(want)) {
		t.Errorf("error %q does not contain %q", err, want)
	}
}

func TestRenderTemplateReadError(t *testing.T) {
	b := &buildCommand{out: &bytes.Buffer{}}
	if _, err := b.renderTemplate("does-not-exist.tmpl", struct{}{}); err == nil {
		t.Fatal("expected read error for missing template, got nil")
	} else if !bytes.Contains([]byte(err.Error()), []byte("reading template")) {
		t.Errorf("error %q does not mention reading failure", err)
	}
}

func TestBuildRunInvalidTemplate(t *testing.T) {
	var out bytes.Buffer
	b := &buildCommand{
		out: &out,
		cfg: BuildConfig{
			Version:   "5.5.0",
			BaseImage: defaultBaseImage,
			Region:    defaultRegion,
			Name:      "fabrica-lore-5.5.0",
			OutputDir: "out",
		},
		mkdirAll:  func(path string, perm os.FileMode) error { return nil },
		writeFile: func(path string, data []byte, perm os.FileMode) error { return nil },
	}
	// Force invalid JSON validation by using a bad base image that breaks the template
	b.cfg.BaseImage = ""
	err := b.run()
	if err == nil {
		t.Error("expected validation error for empty base image, got nil")
	}
}

func TestBuildRunWithPacker(t *testing.T) {
	var out bytes.Buffer
	var writtenFiles []string
	b := &buildCommand{
		out: &out,
		cfg: BuildConfig{
			Version:       "5.5.0",
			BaseImage:     defaultBaseImage,
			Region:        defaultRegion,
			Name:          "fabrica-lore-5.5.0",
			OutputDir:     "out",
			IncludePacker: true,
		},
		mkdirAll: func(path string, perm os.FileMode) error { return nil },
		writeFile: func(path string, data []byte, perm os.FileMode) error {
			writtenFiles = append(writtenFiles, path)
			return nil
		},
	}
	err := b.run()
	if err != nil {
		t.Fatalf("run() unexpected error: %v", err)
	}

	// Should have written 7 files (recipe, component, three shared scripts, packer, build-guide)
	if len(writtenFiles) != 7 {
		t.Errorf("expected 7 files written, got %d: %v", len(writtenFiles), writtenFiles)
	}

	got := out.String()
	if !bytes.Contains([]byte(got), []byte("packer.pkr.hcl")) {
		t.Errorf("expected packer.pkr.hcl in output, got:\n%s", got)
	}
}

func TestBuildRunEmitsSharedContractArtifacts(t *testing.T) {
	outputDir := t.TempDir()
	b := &buildCommand{
		out: &bytes.Buffer{},
		cfg: BuildConfig{
			Version:       "5.5.0",
			BaseImage:     defaultBaseImage,
			Region:        defaultRegion,
			Name:          "fabrica-lore-5.5.0",
			OutputDir:     outputDir,
			IncludePacker: true,
		},
		writeFile: os.WriteFile,
		mkdirAll:  os.MkdirAll,
	}
	if err := b.run(); err != nil {
		t.Fatalf("run() error: %v", err)
	}

	contract := lore.DefaultAMIContract()
	installScript, err := contract.InstallScript("/tmp/lore-bin")
	if err != nil {
		t.Fatalf("InstallScript() error: %v", err)
	}
	bakeVerificationScript, err := contract.BakeVerificationScript()
	if err != nil {
		t.Fatalf("BakeVerificationScript() error: %v", err)
	}
	runtimeVerificationScript, err := contract.VerificationScript()
	if err != nil {
		t.Fatalf("VerificationScript() error: %v", err)
	}

	artifacts := map[string]string{
		"install-lore.sh":            installScript,
		"verify-lore-ami-bake.sh":    bakeVerificationScript,
		"verify-lore-ami-runtime.sh": runtimeVerificationScript,
	}
	for name, want := range artifacts {
		t.Run(name, func(t *testing.T) {
			got, err := os.ReadFile(filepath.Join(outputDir, name))
			if err != nil {
				t.Fatalf("reading %s: %v", name, err)
			}
			if string(got) != want {
				t.Errorf("%s does not match the shared AMI contract", name)
			}
		})
	}

	component, err := os.ReadFile(filepath.Join(outputDir, "component.yaml"))
	if err != nil {
		t.Fatalf("reading component.yaml: %v", err)
	}
	for _, want := range []string{
		"aws s3 sync s3://REPLACE_WITH_YOUR_BUCKET/loreserver-bin/ /tmp/lore-bin/ --exact-timestamps",
		"cp -a /tmp/lore-bin/. /opt/loreserver/",
		"systemctl is-enabled --quiet loreserver.service",
	} {
		if !bytes.Contains(component, []byte(want)) {
			t.Errorf("component.yaml missing shared contract behavior %q", want)
		}
	}

	packer, err := os.ReadFile(filepath.Join(outputDir, "packer.pkr.hcl"))
	if err != nil {
		t.Fatalf("reading packer.pkr.hcl: %v", err)
	}
	for _, want := range []string{"install-lore.sh", "verify-lore-ami-bake.sh"} {
		if !bytes.Contains(packer, []byte(want)) {
			t.Errorf("packer.pkr.hcl does not consume %q", want)
		}
	}
}
