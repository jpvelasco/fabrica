package ops

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestExportDisabled(t *testing.T) {
	c := exportCommand{
		rt:  globals.Runtime{Config: config.Defaults()},
		out: ioDiscard(),
	}
	err := c.run()
	if err == nil || !strings.Contains(err.Error(), "ops is disabled") {
		t.Fatalf("disabled error = %v", err)
	}
}

func TestExportDryRun(t *testing.T) {
	cfg := config.Defaults()
	cfg.Ops.Enabled = true
	cfg.Ops.Modules = []string{"horde"}
	var out bytes.Buffer
	c := exportCommand{rt: globals.Runtime{Config: cfg}, dryRun: true, out: &out}
	if err := c.run(); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, `"module": "horde"`) || !strings.Contains(got, "/fabrica/horde") {
		t.Fatalf("dry-run output missing hook: %s", got)
	}
}

func TestExportWritesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hooks.json")
	cfg := config.Defaults()
	cfg.Ops.Enabled = true
	var out bytes.Buffer
	c := exportCommand{
		rt:        globals.Runtime{Config: cfg},
		output:    path,
		out:       &out,
		writeFile: os.WriteFile,
	}
	if err := c.run(); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "perforce") || !strings.Contains(out.String(), "Wrote") {
		t.Fatalf("file/output incomplete: file=%s out=%s", data, out.String())
	}
}

func TestExportUnknownModule(t *testing.T) {
	cfg := config.Defaults()
	cfg.Ops.Enabled = true
	cfg.Ops.Modules = []string{"mystery"}
	c := exportCommand{rt: globals.Runtime{Config: cfg}, dryRun: true, out: ioDiscard()}
	if err := c.run(); err == nil {
		t.Fatal("expected unknown module error")
	}
}

func ioDiscard() *bytes.Buffer { return &bytes.Buffer{} }
