package ami

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildWritesGuide(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	c := buildCommand{
		out:       &out,
		cfg:       BuildConfig{BaseImage: "ami-0c7217cdde317cfec", Region: "us-east-1", Backend: "zen", OutputDir: "out"},
		writeFile: os.WriteFile,
		mkdirAll:  os.MkdirAll,
	}
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("out", "build-guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "unreal-cloud-ddc") || !strings.Contains(out.String(), "Wrote") {
		t.Fatalf("guide/out incomplete: %s / %s", data, out.String())
	}
}

func TestBuildDryRunAndDefaultDir(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	c := buildCommand{
		out: &out,
		cfg: BuildConfig{BaseImage: "ami-0c7217cdde317cfec", Region: "us-west-2", Backend: "scylla", DryRun: true},
	}
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "backend **scylla**") {
		t.Fatalf("dry-run = %s", out.String())
	}
	c.cfg.DryRun = false
	c.cfg.OutputDir = ""
	c.writeFile = os.WriteFile
	c.mkdirAll = os.MkdirAll
	out.Reset()
	if err := c.run(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(defaultOutputDir, "build-guide.md")); err != nil {
		t.Fatal(err)
	}
}

func TestBuildWriteErrors(t *testing.T) {
	c := buildCommand{
		out:      ioDiscard(),
		cfg:      BuildConfig{BaseImage: "ami-0c7217cdde317cfec", Region: "us-east-1", Backend: "zen", OutputDir: "x"},
		mkdirAll: func(string, os.FileMode) error { return os.ErrPermission },
	}
	if err := c.run(); err == nil {
		t.Fatal("expected mkdir error")
	}
	c.mkdirAll = os.MkdirAll
	c.writeFile = func(string, []byte, os.FileMode) error { return os.ErrPermission }
	if err := c.run(); err == nil {
		t.Fatal("expected write error")
	}
}

func TestNewWiresBuild(t *testing.T) {
	cmd := New(ioDiscard())
	if cmd.Use != "ami" || len(cmd.Commands()) != 1 || cmd.Commands()[0].Use != "build" {
		t.Fatalf("ami tree = %q %v", cmd.Use, cmd.Commands())
	}
}

func TestBuildRejectsBadInput(t *testing.T) {
	c := buildCommand{out: ioDiscard(), cfg: BuildConfig{BaseImage: "not-an-ami", Region: "us-east-1", Backend: "zen"}}
	if err := c.run(); err == nil {
		t.Fatal("expected bad ami")
	}
	c.cfg.BaseImage = "ami-0c7217cdde317cfec"
	c.cfg.Region = "earth"
	if err := c.run(); err == nil {
		t.Fatal("expected bad region")
	}
	c.cfg.Region = "us-east-1"
	c.cfg.Backend = "mongo"
	if err := c.run(); err == nil {
		t.Fatal("expected bad backend")
	}
}

func ioDiscard() *bytes.Buffer { return &bytes.Buffer{} }
