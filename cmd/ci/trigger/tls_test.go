package trigger

import (
	"bytes"
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/horde"
)

func TestTLSTriggerRejectsLegacyProjectBeforeStart(t *testing.T) {
	var out bytes.Buffer
	runner := &fakeRunner{startID: "build"}
	st := provisionedState()
	c := newCmd(&out, runner, st)
	c.buildGraphPath = writeTempBuildGraph(t)
	c.runtime.Config.Horde.TLS = config.HordeTLSConfig{Enabled: true, ServerName: "horde.example.internal", CertPath: "/etc/horde/tls/server.crt", KeyPath: "/etc/horde/tls/server.key", CAPath: "/etc/horde/tls/ca.pem"}
	conn, _ := horde.ConnectionFromConfig(c.runtime.Config.Horde)
	st.GetModule("horde").Resources[0].Properties = map[string]string{horde.ConnectionProperty: conn.Record()}
	if err := c.run(context.Background()); err == nil || runner.startProject != "" {
		t.Fatal("legacy project started", err)
	}
	st.GetModule("ci").Resources[0].Properties = map[string]string{horde.ConnectionProperty: conn.Record(), "hordeCIContract": "1"}
	c.runtime.Config.CI.HordeCAPath = "/etc/horde/tls/ca.pem"
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if runner.startEnv["HORDE_URL"] != conn.URL("") || runner.startEnv["HORDE_CA_FILE"] != "/etc/horde/tls/ca.pem" {
		t.Fatal(runner.startEnv)
	}
	c.runtime.Config.CI.HordeCAPath = "relative"
	if err := c.run(context.Background()); err == nil {
		t.Fatal("invalid CI CA accepted")
	}
}
