package ami

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestCommand(t *testing.T, cfg BuildConfig) (*buildCommand, map[string][]byte, *bytes.Buffer) {
	t.Helper()
	if cfg.OutputDir == "" {
		cfg.OutputDir = t.TempDir()
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	if cfg.BaseImage == "" {
		cfg.BaseImage = "ami-0c7217cdde317cfec"
	}
	written := map[string][]byte{}
	var out bytes.Buffer
	bc := &buildCommand{
		out: &out,
		cfg: cfg,
		writeFile: func(path string, data []byte, perm os.FileMode) error {
			written[filepath.Base(path)] = data
			return nil
		},
		mkdirAll: func(path string, perm os.FileMode) error { return nil },
	}
	return bc, written, &out
}

func TestBuildCommandRun_Docker(t *testing.T) {
	bc, written, _ := newTestCommand(t, BuildConfig{
		Version:   "5.5.0",
		Install:   "docker",
		BaseImage: "ami-0123456789abcdef0",
		Name:      "test-horde",
	})

	if err := bc.run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	for _, name := range []string{"image-builder-recipe.json", "component.yaml", "build-guide.md"} {
		if _, ok := written[name]; !ok {
			t.Errorf("expected %s to be written", name)
		}
	}
	if _, ok := written["packer.pkr.hcl"]; ok {
		t.Error("packer.pkr.hcl should not be written without --include-packer")
	}

	recipe := string(written["image-builder-recipe.json"])
	if !strings.Contains(recipe, "ami-0123456789abcdef0") {
		t.Error("recipe should contain base image AMI ID")
	}
	if !strings.Contains(recipe, "5.5.0") {
		t.Error("recipe should contain Horde version")
	}
	if !strings.Contains(recipe, "REPLACE_WITH_CUSTOM_COMPONENT_ARN") {
		t.Error("recipe should contain placeholder for the custom component ARN")
	}

	component := string(written["component.yaml"])
	if !strings.Contains(component, "InstallDocker") {
		t.Error("docker component should contain InstallDocker step")
	}

	guide := string(written["build-guide.md"])
	if !strings.Contains(guide, "docker") {
		t.Error("build guide should reference docker install method")
	}
}

func TestBuildCommandRun_Native(t *testing.T) {
	bc, written, _ := newTestCommand(t, BuildConfig{
		Version: "5.4.0",
		Install: "native",
		Name:    "test-horde-native",
	})

	if err := bc.run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	component := string(written["component.yaml"])
	if !strings.Contains(component, "InstallDotNet") {
		t.Error("native component should install .NET")
	}
	if strings.Contains(component, "InstallDocker") {
		t.Error("native component should not include InstallDocker step")
	}
}

func TestBuildCommandRun_IncludePacker(t *testing.T) {
	bc, written, _ := newTestCommand(t, BuildConfig{
		Version:       "5.5.0",
		Install:       "docker",
		Name:          "test-horde",
		IncludePacker: true,
	})

	if err := bc.run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	if _, ok := written["packer.pkr.hcl"]; !ok {
		t.Fatal("expected packer.pkr.hcl to be written")
	}
	packer := string(written["packer.pkr.hcl"])
	if !strings.Contains(packer, "5.5.0") {
		t.Error("packer template should contain Horde version")
	}
	if !strings.Contains(packer, "amazon-ebs") {
		t.Error("packer template should reference amazon-ebs builder")
	}
}

func TestBuildCommandRun_DefaultName(t *testing.T) {
	bc, written, _ := newTestCommand(t, BuildConfig{
		Version: "5.5.0",
		Install: "docker",
		Name:    "",
	})

	if err := bc.run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	recipe := string(written["image-builder-recipe.json"])
	if !strings.Contains(recipe, "fabrica-horde-5.5.0") {
		t.Error("default name should be fabrica-horde-<version>")
	}
}

func TestBuildCommandRun_DryRun(t *testing.T) {
	bc, written, out := newTestCommand(t, BuildConfig{
		Version:       "5.5.0",
		Install:       "docker",
		Name:          "test-horde",
		IncludePacker: true,
		DryRun:        true,
	})

	if err := bc.run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(written) != 0 {
		t.Errorf("dry-run wrote %d files; want 0: %v", len(written), keysOf(written))
	}

	s := out.String()
	for _, name := range []string{"image-builder-recipe.json", "component.yaml", "packer.pkr.hcl", "build-guide.md"} {
		if !strings.Contains(s, name) {
			t.Errorf("dry-run output should mention %s", name)
		}
	}
	if !strings.Contains(s, "Dry run") {
		t.Error("dry-run output should announce the dry run")
	}
}

func TestBuildCommandRun_WriteError(t *testing.T) {
	bc, _, _ := newTestCommand(t, BuildConfig{
		Version: "5.5.0",
		Install: "docker",
		Name:    "test",
	})
	bc.writeFile = func(path string, data []byte, perm os.FileMode) error {
		return errors.New("disk full")
	}

	err := bc.run()
	if err == nil {
		t.Fatal("expected error when writeFile fails")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("error should propagate write failure: %v", err)
	}
}

func TestBuildCommandRun_MkdirError(t *testing.T) {
	bc, _, _ := newTestCommand(t, BuildConfig{
		Version: "5.5.0",
		Install: "docker",
	})
	bc.mkdirAll = func(path string, perm os.FileMode) error {
		return errors.New("permission denied")
	}

	err := bc.run()
	if err == nil {
		t.Fatal("expected error when mkdirAll fails")
	}
	if !strings.Contains(err.Error(), "--output-dir") {
		t.Errorf("error should name the offending flag: %v", err)
	}
}

func TestBuildCommandValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     BuildConfig
		wantErr string
	}{
		{
			name:    "empty version",
			cfg:     BuildConfig{Version: "", Install: "docker", BaseImage: "ami-0123456789abcdef", Region: "us-east-1", OutputDir: "x"},
			wantErr: "--horde-version is required",
		},
		{
			name:    "bad version",
			cfg:     BuildConfig{Version: "5.x", Install: "docker", BaseImage: "ami-0123456789abcdef", Region: "us-east-1", OutputDir: "x"},
			wantErr: "--horde-version must be in the format",
		},
		{
			name:    "version latest",
			cfg:     BuildConfig{Version: "latest", Install: "docker", BaseImage: "ami-0123456789abcdef", Region: "us-east-1", OutputDir: "x"},
			wantErr: "",
		},
		{
			name:    "version short",
			cfg:     BuildConfig{Version: "5.5", Install: "docker", BaseImage: "ami-0123456789abcdef", Region: "us-east-1", OutputDir: "x"},
			wantErr: "",
		},
		{
			name:    "bad install",
			cfg:     BuildConfig{Version: "5.5.0", Install: "podman", BaseImage: "ami-0123456789abcdef", Region: "us-east-1", OutputDir: "x"},
			wantErr: "--install must be",
		},
		{
			name:    "bad base image",
			cfg:     BuildConfig{Version: "5.5.0", Install: "docker", BaseImage: "bogus", Region: "us-east-1", OutputDir: "x"},
			wantErr: "--base-image must be a valid AMI ID",
		},
		{
			name:    "bad region",
			cfg:     BuildConfig{Version: "5.5.0", Install: "docker", BaseImage: "ami-0123456789abcdef", Region: "bogus", OutputDir: "x"},
			wantErr: "--region must be a valid AWS region",
		},
		{
			name:    "name with slash",
			cfg:     BuildConfig{Version: "5.5.0", Install: "docker", BaseImage: "ami-0123456789abcdef", Region: "us-east-1", Name: "../escape", OutputDir: "x"},
			wantErr: "--name can only contain",
		},
		{
			name:    "name too long",
			cfg:     BuildConfig{Version: "5.5.0", Install: "docker", BaseImage: "ami-0123456789abcdef", Region: "us-east-1", Name: strings.Repeat("a", 128), OutputDir: "x"},
			wantErr: "--name must be",
		},
		{
			name:    "empty output dir",
			cfg:     BuildConfig{Version: "5.5.0", Install: "docker", BaseImage: "ami-0123456789abcdef", Region: "us-east-1", OutputDir: ""},
			wantErr: "--output-dir is required",
		},
		{
			name:    "valid",
			cfg:     BuildConfig{Version: "5.5.0", Install: "native", BaseImage: "ami-0123456789abcdef", Region: "us-west-2", Name: "fabrica-horde", OutputDir: "x"},
			wantErr: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bc := &buildCommand{cfg: tc.cfg}
			err := bc.validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestValidateImageBuilderJSON(t *testing.T) {
	valid := []byte(`{
		"name": "test",
		"semanticVersion": "1.0.0",
		"parentImage": "ami-0123456789abcdef",
		"components": [
			{"componentArn": "arn:aws:imagebuilder:us-east-1:aws:component/update-linux/1.0.2/1"},
			{"componentArn": "REPLACE_WITH_CUSTOM_COMPONENT_ARN"}
		]
	}`)
	if err := validateImageBuilderJSON(valid); err != nil {
		t.Errorf("unexpected error on valid JSON: %v", err)
	}

	cases := map[string][]byte{
		"missing name":             []byte(`{"semanticVersion": "1.0.0", "parentImage": "ami-0", "components": [{"componentArn": "arn:aws:imagebuilder:x:aws:component/y"}]}`),
		"missing semantic version": []byte(`{"name": "x", "parentImage": "ami-0", "components": [{"componentArn": "arn:aws:imagebuilder:x:aws:component/y"}]}`),
		"missing parent":           []byte(`{"name": "x", "semanticVersion": "1.0.0", "components": [{"componentArn": "arn:aws:imagebuilder:x:aws:component/y"}]}`),
		"empty components":         []byte(`{"name": "x", "semanticVersion": "1.0.0", "parentImage": "ami-0", "components": []}`),
		"non-arn component":        []byte(`{"name": "x", "semanticVersion": "1.0.0", "parentImage": "ami-0", "components": [{"componentArn": "not-an-arn"}]}`),
		"empty arn":                []byte(`{"name": "x", "semanticVersion": "1.0.0", "parentImage": "ami-0", "components": [{"componentArn": ""}]}`),
		"invalid json":             []byte(`not json`),
	}
	for name, data := range cases {
		if err := validateImageBuilderJSON(data); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestValidateComponentYAML(t *testing.T) {
	valid := []byte("name: test\nschemaVersion: 1.0\nphases:\n  - name: build\n")
	if err := validateComponentYAML(valid); err != nil {
		t.Errorf("unexpected error on valid YAML: %v", err)
	}

	cases := map[string][]byte{
		"missing schemaVersion": []byte("name: test\nphases:\n  - name: build\n"),
		"missing phases":        []byte("name: test\nschemaVersion: 1.0\n"),
		"missing name":          []byte("schemaVersion: 1.0\nphases:\n  - name: build\n"),
	}
	for label, data := range cases {
		if err := validateComponentYAML(data); err == nil {
			t.Errorf("%s: expected error, got nil", label)
		}
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
