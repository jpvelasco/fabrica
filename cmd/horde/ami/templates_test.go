package ami

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderImageBuilderTemplate_Docker(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:   "5.5.0",
		Install:   "docker",
		BaseImage: "ami-abc123",
		Name:      "fabrica-horde-5.5.0",
	}}

	out, err := bc.renderTemplate("image-builder.json.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	// Validate it's parseable JSON
	var raw map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("rendered template is not valid JSON: %v\n%s", err, string(out))
	}

	if raw["parentImage"] != "ami-abc123" {
		t.Errorf("parentImage = %v, want ami-abc123", raw["parentImage"])
	}

	s := string(out)
	if !strings.Contains(s, "docker") {
		t.Error("docker template should reference docker installation method")
	}
}

func TestRenderImageBuilderTemplate_Native(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:   "5.4.0",
		Install:   "native",
		BaseImage: "ami-native",
		Name:      "fabrica-horde-native",
	}}

	out, err := bc.renderTemplate("image-builder.json.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("rendered native template is not valid JSON: %v\n%s", err, string(out))
	}

	s := string(out)
	if strings.Contains(s, "docker-ce-ubuntu") {
		t.Error("native template should not include docker component")
	}
	if !strings.Contains(s, "native-component") {
		t.Error("native template should include native component")
	}
}

func TestRenderPackerTemplate_Docker(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:   "5.5.0",
		Install:   "docker",
		BaseImage: "ami-packer123",
		Name:      "fabrica-horde-packer",
	}}

	out, err := bc.renderTemplate("packer.hcl.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	s := string(out)
	if !strings.Contains(s, "ami-packer123") {
		t.Error("packer template should contain base image ID")
	}
	if !strings.Contains(s, "5.5.0") {
		t.Error("packer template should contain horde version")
	}
	if !strings.Contains(s, "amazon-ebs") {
		t.Error("packer template should use amazon-ebs builder")
	}
	if !strings.Contains(s, "GITHUB_PAT") {
		t.Error("docker packer template should reference GITHUB_PAT")
	}
}

func TestRenderPackerTemplate_Native(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:   "5.5.0",
		Install:   "native",
		BaseImage: "ami-native456",
		Name:      "fabrica-horde-native",
	}}

	out, err := bc.renderTemplate("packer.hcl.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	s := string(out)
	if strings.Contains(s, "GITHUB_PAT") {
		t.Error("native packer template should not reference GITHUB_PAT")
	}
	if !strings.Contains(s, "dotnet-sdk-8.0") {
		t.Error("native packer template should install .NET 8")
	}
}

func TestRenderBuildGuideTemplate_Docker(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:       "5.5.0",
		Install:       "docker",
		BaseImage:     "ami-guide",
		Name:          "test-horde",
		IncludePacker: true,
	}}

	out, err := bc.renderTemplate("build-guide.md.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	s := string(out)
	if !strings.Contains(s, "Docker") {
		t.Error("docker build guide should mention Docker")
	}
	if !strings.Contains(s, "packer.pkr.hcl") {
		t.Error("build guide with IncludePacker should mention packer.pkr.hcl")
	}
}

func TestRenderBuildGuideTemplate_NativeNoPackerFile(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:       "5.5.0",
		Install:       "native",
		BaseImage:     "ami-guide",
		Name:          "test-horde",
		IncludePacker: false,
	}}

	out, err := bc.renderTemplate("build-guide.md.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	s := string(out)
	if strings.Contains(s, "packer.pkr.hcl") {
		t.Error("build guide without IncludePacker should not mention packer.pkr.hcl")
	}
}
