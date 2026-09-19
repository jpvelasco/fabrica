package ami

import (
	"bytes"
	"errors"
	"io"
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
		`dcvdir=$(find . -maxdepth 1 -type d -name 'nice-dcv-*' | head -n1)`,
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

func TestBuildValidateEmptyFields(t *testing.T) {
	c := buildCommand{cfg: BuildConfig{BaseImage: defaultBaseImage, Region: "us-west-2", Name: "", OutputDir: "out"}}
	if err := c.validate(); err == nil {
		t.Fatal("expected empty name error")
	}
	c.cfg.Name = "ok"
	c.cfg.OutputDir = ""
	if err := c.validate(); err == nil {
		t.Fatal("expected empty output-dir error")
	}
}

func TestBuildMkdirError(t *testing.T) {
	c := buildCommand{
		out:      io.Discard,
		cfg:      BuildConfig{BaseImage: defaultBaseImage, Region: "us-west-2", Name: "x", OutputDir: "out"},
		mkdirAll: func(string, os.FileMode) error { return errors.New("mkdir boom") },
	}
	err := c.run()
	if err == nil || !strings.Contains(err.Error(), "creating --output-dir") {
		t.Fatalf("got %v", err)
	}
}

func TestBuildWriteError(t *testing.T) {
	c := buildCommand{
		out:       io.Discard,
		cfg:       BuildConfig{BaseImage: defaultBaseImage, Region: "us-west-2", Name: "x", OutputDir: t.TempDir()},
		mkdirAll:  os.MkdirAll,
		writeFile: func(string, []byte, os.FileMode) error { return errors.New("write boom") },
	}
	err := c.run()
	if err == nil || !strings.Contains(err.Error(), "writing") {
		t.Fatalf("got %v", err)
	}
}

func TestRenderMissingTemplate(t *testing.T) {
	c := buildCommand{}
	_, err := c.render("no-such.tmpl", struct{}{})
	if err == nil || !strings.Contains(err.Error(), "reading template") {
		t.Fatalf("got %v", err)
	}
}

func TestRenderExecuteError(t *testing.T) {
	c := buildCommand{}
	_, err := c.render("component.yaml.tmpl", struct{}{})
	if err == nil || !strings.Contains(err.Error(), "rendering template") {
		t.Fatalf("got %v", err)
	}
}
