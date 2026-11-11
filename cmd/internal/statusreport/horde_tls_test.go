package statusreport

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/state"
)

func TestHordeProbeLabelsTLSUnverified(t *testing.T) {
	cfg := config.Defaults()
	cfg.Horde.Port = 8443
	cfg.Horde.TLS = config.HordeTLSConfig{Enabled: true, ServerName: "horde.example.internal", CertPath: "/etc/horde/tls/server.crt", KeyPath: "/etc/horde/tls/server.key", CAPath: "/etc/horde/tls/ca.pem"}
	c, _ := horde.ConnectionFromConfig(cfg.Horde)
	m := &state.ModuleState{Name: "horde", Resources: []state.ModuleResource{{TypeName: "AWS::EC2::Instance", Properties: map[string]string{horde.ConnectionProperty: c.Record()}}}}
	for _, up := range []bool{true, false} {
		result := probeModuleState(m, cfg, "10.0.1.2", func(addr string) bool {
			if addr != "10.0.1.2:8443" {
				t.Fatal("wrong port", addr)
			}
			return up
		})
		if !strings.Contains(result, "TLS unverified") {
			t.Fatal(result)
		}
	}
	cfg.Horde.TLS = config.HordeTLSConfig{}
	result := probeModuleState(m, cfg, "10.0.1.2", func(string) bool { t.Fatal("probed before state mismatch"); return false })
	if !strings.Contains(result, "configuration error") {
		t.Fatal(result)
	}
	result = probeModuleState(m, nil, "10.0.1.2", func(string) bool { t.Fatal("probed with missing TLS config"); return false })
	if !strings.Contains(result, "configuration error") {
		t.Fatal(result)
	}
}
