package drift

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/state"
)

func TestHordeTLSRecoveryPreservesTransport(t *testing.T) {
	mod := hordeModuleFixture()
	cfg := config.Defaults()
	cfg.Horde.TLS = config.HordeTLSConfig{Enabled: true, ServerName: "horde.example.internal", CertPath: "/etc/horde/tls/server.crt", KeyPath: "/etc/horde/tls/server.key", CAPath: "/etc/horde/tls/ca.pem"}
	c, _ := horde.ConnectionFromConfig(cfg.Horde)
	for i := range mod.Resources {
		if horde.IsCoordinator(mod.Resources[i]) {
			if mod.Resources[i].Properties == nil {
				mod.Resources[i].Properties = map[string]string{}
			}
			mod.Resources[i].Properties[horde.ConnectionProperty] = c.Record()
		}
	}
	withHordePassword(t, func(string) (string, error) { return "not-rendered", nil })
	ds, err := rebuildInstanceDesiredState(context.Background(), &mod.Resources[2], mod, state.NewState("", ""), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ud := decodedUserData(t, ds)
	if !strings.Contains(ud, c.URL("")) || !strings.Contains(ud, "--cacert") {
		t.Fatal("TLS lost")
	}
	sg := rebuildSGDoc(t, &mod.Resources[0], mod, cfg)
	ingress := sg["SecurityGroupIngress"].([]any)
	if len(ingress) != 1 {
		t.Fatal("plaintext gRPC recreated", ingress)
	}
	cfg.Horde.TLS = config.HordeTLSConfig{}
	if _, err := rebuildHordeInstance(&mod.Resources[2], mod, cfg); err == nil {
		t.Fatal("instance downgrade")
	}
	if _, err := rebuildSGDesiredState(&mod.Resources[0], mod, cfg); err == nil {
		t.Fatal("SG downgrade")
	}
}
