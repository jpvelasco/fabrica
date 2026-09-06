package ops

import (
	"bytes"
	"os"
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
	t.Chdir(t.TempDir())
	cfg := config.Defaults()
	cfg.Ops.Enabled = true
	var out bytes.Buffer
	c := exportCommand{
		rt:        globals.Runtime{Config: cfg},
		output:    "hooks.json",
		out:       &out,
		writeFile: os.WriteFile,
		mkdirAll:  os.MkdirAll,
	}
	if err := c.run(); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile("hooks.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "perforce") || !strings.Contains(out.String(), "Wrote") {
		t.Fatalf("file/output incomplete: file=%s out=%s", data, out.String())
	}
}

func TestExportJSONOut(t *testing.T) {
	cfg := config.Defaults()
	cfg.Ops.Enabled = true
	cfg.Ops.Modules = []string{"ci"}
	var out bytes.Buffer
	c := exportCommand{rt: globals.Runtime{Config: cfg}, jsonOut: true, out: &out}
	if err := c.run(); err != nil {
		t.Fatalf("json: %v", err)
	}
	if !strings.Contains(out.String(), `"module": "ci"`) {
		t.Fatalf("json output: %s", out.String())
	}
}

func TestExportWriteError(t *testing.T) {
	cfg := config.Defaults()
	cfg.Ops.Enabled = true
	c := exportCommand{
		rt:     globals.Runtime{Config: cfg},
		output: "hooks.json",
		out:    ioDiscard(),
		writeFile: func(string, []byte, os.FileMode) error {
			return os.ErrPermission
		},
	}
	if err := c.run(); err == nil || !strings.Contains(err.Error(), "writing") {
		t.Fatalf("write error = %v", err)
	}
}

func TestExportMkdirError(t *testing.T) {
	cfg := config.Defaults()
	cfg.Ops.Enabled = true
	c := exportCommand{
		rt:     globals.Runtime{Config: cfg},
		output: "nested/hooks.json",
		out:    ioDiscard(),
		mkdirAll: func(string, os.FileMode) error {
			return os.ErrPermission
		},
	}
	if err := c.run(); err == nil || !strings.Contains(err.Error(), "creating ops export dir") {
		t.Fatalf("mkdir error = %v", err)
	}
}

func TestExportRejectsAbsolutePath(t *testing.T) {
	cfg := config.Defaults()
	cfg.Ops.Enabled = true
	c := exportCommand{rt: globals.Runtime{Config: cfg}, output: "/tmp/hooks.json", out: ioDiscard()}
	if err := c.run(); err == nil || !strings.Contains(err.Error(), "relative path") {
		t.Fatalf("abs path error = %v", err)
	}
}

func TestExportDefaultPath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	cfg := config.Defaults()
	cfg.Ops.Enabled = true
	cfg.Ops.Modules = []string{"ddc"}
	var out bytes.Buffer
	c := exportCommand{
		rt:        globals.Runtime{Config: cfg},
		out:       &out,
		writeFile: os.WriteFile,
		mkdirAll:  os.MkdirAll,
	}
	if err := c.run(); err != nil {
		t.Fatalf("default path: %v", err)
	}
	if _, err := os.Stat(defaultOutput); err != nil {
		t.Fatalf("expected %s: %v", defaultOutput, err)
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
