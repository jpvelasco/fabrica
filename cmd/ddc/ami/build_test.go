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
