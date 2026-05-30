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
		Region:    "us-east-1",
		Name:      "fabrica-horde-5.5.0",
	}}

	out, err := bc.renderTemplate("image-builder.json.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("rendered template is not valid JSON: %v\n%s", err, string(out))
	}

	if raw["parentImage"] != "ami-abc123" {
		t.Errorf("parentImage = %v, want ami-abc123", raw["parentImage"])
	}

	s := string(out)
	if !strings.Contains(s, "arn:aws:imagebuilder:us-east-1:aws:component/update-linux/") {
		t.Error("recipe should contain real update-linux ARN")
	}
	if !strings.Contains(s, "REPLACE_WITH_CUSTOM_COMPONENT_ARN") {
		t.Error("recipe should contain placeholder for custom component ARN")
	}
	if strings.Contains(s, "_comment") {
		t.Error("recipe must not contain _comment (not part of Image Builder schema)")
	}
}

func TestRenderImageBuilderTemplate_Region(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:   "5.5.0",
		Install:   "docker",
		BaseImage: "ami-abc123",
		Region:    "eu-west-1",
		Name:      "test",
	}}

	out, err := bc.renderTemplate("image-builder.json.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	s := string(out)
	if !strings.Contains(s, "eu-west-1") {
		t.Error("recipe should contain the configured region in component ARNs")
	}
	if strings.Contains(s, "us-east-1") {
		t.Error("recipe should not hardcode us-east-1 when region is eu-west-1")
	}
}

func TestRenderComponentTemplate_Docker(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version: "5.5.0",
		Install: "docker",
		Name:    "test-horde",
	}}

	out, err := bc.renderTemplate("component.yaml.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	s := string(out)
	if !strings.Contains(s, "schemaVersion: 1.0") {
		t.Error("component should have schemaVersion: 1.0")
	}
	if !strings.Contains(s, "InstallDocker") {
		t.Error("docker component should have InstallDocker step")
	}
	if !strings.Contains(s, "InstallHordeSystemdUnit") {
		t.Error("docker component should have InstallHordeSystemdUnit step")
	}
	if strings.Contains(s, "InstallDotNet") {
		t.Error("docker component should not have InstallDotNet step")
	}
}

func TestRenderComponentTemplate_Native(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version: "5.4.0",
		Install: "native",
		Name:    "test-horde-native",
	}}

	out, err := bc.renderTemplate("component.yaml.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	s := string(out)
	if !strings.Contains(s, "InstallDotNet") {
		t.Error("native component should have InstallDotNet step")
	}
	if !strings.Contains(s, "InstallMongoDB") {
		t.Error("native component should have InstallMongoDB step")
	}
	if !strings.Contains(s, "InstallRedis") {
		t.Error("native component should have InstallRedis step")
	}
	if !strings.Contains(s, "InstallHordeBinary") {
		t.Error("native component should have InstallHordeBinary step")
	}
	if strings.Contains(s, "InstallDocker") {
		t.Error("native component should not have InstallDocker step")
	}
}

func TestRenderPackerTemplate_Docker(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:   "5.5.0",
		Install:   "docker",
		BaseImage: "ami-packer123",
		Region:    "us-west-2",
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
	if !strings.Contains(s, "us-west-2") {
		t.Error("packer template should contain the configured region")
	}
	if strings.Contains(s, "GITHUB_PAT") {
		t.Error("packer template should not reference GITHUB_PAT")
	}
	// Ensure no # comments inside inline = [...] list literals (invalid HCL)
	inInlineList := false
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "inline = [") {
			inInlineList = true
		}
		if inInlineList && trimmed == "]" {
			inInlineList = false
		}
		if inInlineList && strings.HasPrefix(trimmed, "#") {
			t.Errorf("packer template must not contain comment lines inside inline lists (invalid HCL): %q", trimmed)
		}
	}
}

func TestRenderPackerTemplate_Native(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:   "5.5.0",
		Install:   "native",
		BaseImage: "ami-native456",
		Region:    "us-east-1",
		Name:      "fabrica-horde-native",
	}}

	out, err := bc.renderTemplate("packer.hcl.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	s := string(out)
	if !strings.Contains(s, "dotnet-sdk-8.0") {
		t.Error("native packer template should install .NET 8")
	}
	if !strings.Contains(s, "horde_source_dir") {
		t.Error("native packer template should declare horde_source_dir variable")
	}
	if strings.Contains(s, "GITHUB_PAT") {
		t.Error("native packer template should not reference GITHUB_PAT")
	}
}

func TestRenderBuildGuideTemplate_Docker(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:       "5.5.0",
		Install:       "docker",
		BaseImage:     "ami-guide",
		Region:        "us-east-1",
		Name:          "test-horde",
		IncludePacker: true,
	}}

	out, err := bc.renderTemplate("build-guide.md.tmpl", bc.cfg)
	if err != nil {
		t.Fatalf("renderTemplate error: %v", err)
	}

	s := string(out)
	if !strings.Contains(s, "docker install") {
		t.Error("docker build guide should mention docker install method")
	}
	if !strings.Contains(s, "packer.pkr.hcl") {
		t.Error("build guide with IncludePacker should mention packer.pkr.hcl")
	}
	if !strings.Contains(s, "component.yaml") {
		t.Error("build guide should mention component.yaml")
	}
	if !strings.Contains(s, "aws imagebuilder create-component") {
		t.Error("build guide should include create-component command")
	}
}

func TestRenderBuildGuideTemplate_NativeNoPackerFile(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:       "5.5.0",
		Install:       "native",
		BaseImage:     "ami-guide",
		Region:        "us-east-1",
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
	if !strings.Contains(s, "native install") {
		t.Error("native build guide should mention native install method")
	}
}
