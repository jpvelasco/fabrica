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
		// Pinned versioned tarball (2026.0-24952) with a hard SHA256 check
		// and a fingerprint-gated GPG key import (#490).
		"https://d1uj6qtbmh3dt5.cloudfront.net/2026.0/Servers/nice-dcv-2026.0-24952-ubuntu2204-x86_64.tgz",
		"52651a93844ce3d5ad918ee7666b007bb2a1c7ff7cf68dc50692e6b18efaa35a",
		"sha256sum -c -",
		"5B9EEBC864449701F6CE566A11B5C70A170C6114",
		// dpkg-sig verifies each .deb's GpgBuilder signature before install.
		"dpkg-sig --verify",
		// x11-xserver-utils provides xsetroot and dbus-x11 provides
		// dbus-launch on jammy (plain dbus does not); both are required for
		// the DCV virtual session to survive boot (#489). xterm is the
		// terminal emulator Xsession requires when no session manager, window
		// manager, or user ~/.xsession is present (#492).
		"apt-get install -y wget gnupg dpkg-sig xvfb x11-xserver-utils dbus-x11 xdg-user-dirs xterm",
		`dcvdir=$(find . -maxdepth 1 -type d -name 'nice-dcv-*' | head -n1)`,
		"systemctl enable dcvserver",
		"command -v dcv",
		"command -v xsetroot",
		"command -v dbus-launch",
		"command -v xterm",
		"snap install amazon-ssm-agent --classic",
		"amazon-ssm-agent.service",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("component missing %q", want)
		}
	}
	// The unversioned "latest" tarball URL must not remain — the build is
	// pinned to the 2026.0-24952 release path.
	if strings.Contains(s, "https://d1uj6qtbmh3dt5.cloudfront.net/nice-dcv-ubuntu2204-x86_64.tgz") {
		t.Error("component must not use the unversioned DCV tarball URL")
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
