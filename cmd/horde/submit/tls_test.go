package submit

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/horde"
)

func TestTLSValidationBeforeTokenAndDryRun(t *testing.T) {
	var out bytes.Buffer
	c, _, _ := tokenTestCommand(t, &out, testTokenParam, func(context.Context, string) (string, error) {
		t.Fatal("read token before TLS validation")
		return "", nil
	})
	c.runtime.Config.Horde.TLS = config.HordeTLSConfig{Enabled: true, ServerName: "horde.example.internal", CertPath: "/etc/horde/tls/server.crt", KeyPath: "/etc/horde/tls/server.key", CAPath: "/etc/horde/tls/ca.pem"}
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "adopt") {
		t.Fatal("unverified TLS sent token", err)
	}
	connection, _ := horde.ConnectionFromConfig(c.runtime.Config.Horde)
	st, _ := c.readState()
	st.GetModule("horde").Resources[0].Properties[horde.ConnectionProperty] = connection.Record()
	c.runtime.Config.Horde.ClientCAPath = "missing.pem"
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "clientCAPath") {
		t.Fatal("missing CA sent token", err)
	}
	c.dryRun = true
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), connection.URL("")) {
		t.Fatal("dry-run URL", out.String())
	}
	// Matching TLS dry-run resolves state while deliberately not opening CA
	// files or fetching credentials.
}
