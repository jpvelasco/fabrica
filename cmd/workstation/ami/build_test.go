package ami

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildValidate(t *testing.T) {
	c := buildCommand{cfg: BuildConfig{BaseImage: "not-ami", Region: "us-west-2", Name: "x", OutputDir: "out"}}
	if err := c.validate(); err == nil {
		t.Fatal("expected invalid AMI")
	}
	c.cfg.BaseImage = defaultBaseImage
	c.cfg.Region = "not-a-region"
	if err := c.validate(); err == nil {
		t.Fatal("expected invalid region")
	}
	c.cfg.Region = "us-west-2"
	c.cfg.Name = "bad name"
	if err := c.validate(); err == nil {
		t.Fatal("expected invalid name")
	}
}

func TestBuildWritesDCVAndSSM(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	c := buildCommand{
		out: &out,
		cfg: BuildConfig{
			BaseImage: "ami-0bdb09211df876db4",
			Region:    "us-west-2",
			Name:      "fabrica-workstation-dcv",
			OutputDir: dir,
		},
		writeFile: os.WriteFile,
		mkdirAll:  os.MkdirAll,
	}
	if err := c.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	comp, err := os.ReadFile(filepath.Join(dir, "component.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(comp)
	for _, want := range []string{
		"nice-dcv-ubuntu2204-x86_64.tgz",
		"systemctl enable dcvserver",
		"command -v dcv",
		"snap install amazon-ssm-agent --classic",
		"amazon-ssm-agent.service",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("component missing %q", want)
		}
	}
	if strings.Contains(s, "|| true") {
		t.Error("component must not soft-fail SSM")
	}
	recipe, err := os.ReadFile(filepath.Join(dir, "image-builder-recipe.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(recipe), `"uninstallAfterBuild": false`) {
		t.Error("recipe must keep SSM agent after bake")
	}
}

func TestIndent(t *testing.T) {
	got := indent(2, "a\nb\n")
	if got != "  a\n  b" {
		t.Errorf("indent = %q", got)
	}
}
