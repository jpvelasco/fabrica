package doctorchecks

import (
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestHordeStructuralValidation(t *testing.T) {
	cfg := config.Defaults()
	cfg.Horde.TLS = config.HordeTLSConfig{Enabled: true, ServerName: "horde.example.internal", CertPath: "/etc/horde/tls/server.crt", KeyPath: "/etc/horde/tls/server.key", CAPath: "/etc/horde/tls/ca.pem"}
	rt := globals.Runtime{Config: cfg}
	if result := checkHordeConnection(rt); result.Status != "ok" {
		t.Fatal("doctor checked remote files on laptop", result)
	}
	cfg.Horde.TLS.ServerName = "https://invalid"
	if result := checkHordeConnection(rt); result.Status != "fail" {
		t.Fatal(result)
	}
	cfg.Horde = config.HordeConfig{}
	cfg.CI.HordeCAPath = "relative"
	if result := checkHordeConnection(rt); result.Status != "fail" {
		t.Fatal(result)
	}
}
