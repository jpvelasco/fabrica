package configcmd

import (
	"bytes"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestConfigShowRefusesInvalidNamePrefix(t *testing.T) {
	cfg := config.Defaults()
	cfg.Cloud.AWS.Identity.NamePrefix = "Bad_Prefix"
	if err := (showCommand{runtime: globals.Runtime{Config: cfg}, out: &bytes.Buffer{}}).run(); err == nil {
		t.Fatal("gated name prefix shown as resolved")
	}
}
