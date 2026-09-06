package backup

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestRunScheduleDisabled(t *testing.T) {
	var out bytes.Buffer
	if err := runSchedule(globals.Runtime{Config: config.Defaults()}, false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "disabled") {
		t.Fatalf("got %s", out.String())
	}
}

func TestRunScheduleEnabledJSON(t *testing.T) {
	cfg := config.Defaults()
	cfg.Perforce.Backup.Schedule = "15 3 * * *"
	cfg.Perforce.Backup.Retain = 4
	var out bytes.Buffer
	if err := runSchedule(globals.Runtime{Config: cfg}, true, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"cron": "15 3 * * *"`) {
		t.Fatalf("json = %s", out.String())
	}
	out.Reset()
	if err := runSchedule(globals.Runtime{Config: cfg}, false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "retain:  4") {
		t.Fatalf("text = %s", out.String())
	}
}

func TestRunScheduleInvalid(t *testing.T) {
	cfg := config.Defaults()
	cfg.Perforce.Backup.Schedule = "bad"
	if err := runSchedule(globals.Runtime{Config: cfg}, false, ioDiscard()); err == nil {
		t.Fatal("expected invalid schedule")
	}
}

func TestRunVerify(t *testing.T) {
	var out bytes.Buffer
	if err := runVerify(globals.Runtime{Config: config.Defaults()}, "ckpt-1", false, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "ckpt-1") || !strings.Contains(out.String(), "restore") {
		t.Fatalf("verify = %s", out.String())
	}
	out.Reset()
	if err := runVerify(globals.Runtime{Config: config.Defaults()}, "ckpt-1", true, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"backupId"`) {
		t.Fatalf("json = %s", out.String())
	}
	if err := runVerify(globals.Runtime{Config: config.Defaults()}, "???", false, ioDiscard()); err == nil {
		t.Fatal("expected empty-id error")
	}
}

func ioDiscard() *bytes.Buffer { return &bytes.Buffer{} }
