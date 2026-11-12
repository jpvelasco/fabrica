package export

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/state"
)

func TestExportPreservesTLSListenerAndOmitsInternalContract(t *testing.T) {
	cfg := config.Defaults()
	cfg.Horde.TLS = config.HordeTLSConfig{Enabled: true, ServerName: "horde.example.internal", CertPath: "/etc/horde/tls/server.crt", KeyPath: "/etc/horde/tls/server.key", CAPath: "/etc/horde/tls/ca.pem"}
	cfg.Horde.Port = 8443
	c, _ := horde.ConnectionFromConfig(cfg.Horde)
	cfg.Horde.AllowedCIDR = "10.20.0.0/16"
	st := state.NewState("", "")
	st.UpsertModule("horde", "image", "ready", []state.ModuleResource{{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-coordinator", Properties: map[string]string{horde.ConnectionProperty: c.Record()}}})
	// An absent/changed local config must never cause a recorded TLS SG to
	// export the old plaintext ports.
	for _, format := range []Format{CloudFormation, Terraform} {
		data, err := GenerateOutput(format, st, config.Defaults())
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, "8443") || strings.Contains(text, "5002") || strings.Contains(text, horde.ConnectionProperty) || !strings.Contains(text, "not reconstructed") {
			t.Fatal(text)
		}
	}
	data, err := GenerateOutput(CloudFormation, st, cfg)
	if err != nil || !strings.Contains(string(data), "10.20.0.0/16") {
		t.Fatalf("configured ingress lost: %s %v", data, err)
	}
	st.Modules[0].Resources[0].Properties[horde.ConnectionProperty] = "bad"
	if _, err := GenerateOutput(CloudFormation, st, cfg); err == nil {
		t.Fatal("corrupt transport exported")
	}
}
