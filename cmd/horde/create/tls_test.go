package create

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/state"
)

func TestTLSCreateRecordsAndAppliesOneConnection(t *testing.T) {
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	provider := &testutil.InspectorProvider{}
	st := testutil.NewTestState()
	c := newTestCommand(&out, provider, st)
	c.assumeYes = true
	c.runtime.Config.Horde.TLS = config.HordeTLSConfig{Enabled: true, ServerName: "horde.example.internal", CertPath: "/etc/horde/tls/server.crt", KeyPath: "/etc/horde/tls/server.key", CAPath: "/etc/horde/tls/ca.pem"}
	expected, err := horde.ConnectionFromConfig(c.runtime.Config.Horde)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if provider.CreateCalls != 4 {
		t.Fatalf("created %d resources", provider.CreateCalls)
	}
	for _, r := range st.GetModule("horde").Resources {
		if horde.IsCoordinator(r) && r.Properties[horde.ConnectionProperty] != expected.Record() {
			t.Fatal("coordinator resource lost its connection")
		}
	}
	for _, r := range provider.Created {
		if r.TypeName != cloud.TypeAWSEC2Instance {
			continue
		}
		var instance struct{ UserData string }
		if err := json.Unmarshal(r.DesiredState, &instance); err != nil {
			t.Fatal(err)
		}
		script, err := base64.StdEncoding.DecodeString(instance.UserData)
		if err != nil || !strings.Contains(string(script), expected.URL("")) || !strings.Contains(string(script), `"Horde__HttpPort": "0"`) {
			t.Fatalf("instance did not receive TLS bootstrap: %v", err)
		}
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "Horde gRPC:") && strings.HasSuffix(line, expected.URL("")) {
			return
		}
	}
	t.Fatal("gRPC output did not use the shared TLS endpoint")
}

func TestTLSCreateRefusesUnadoptedStateAndInvalidIdentity(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		var out bytes.Buffer
		provider := &testutil.InspectorProvider{}
		st := testutil.NewTestState()
		st.UpsertModule("horde", "ami-existing", "ready", []state.ModuleResource{{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-existing"}})
		c := newTestCommand(&out, provider, st)
		c.runtime.Config.Horde.TLS = config.HordeTLSConfig{Enabled: true, ServerName: "horde.example.internal", CertPath: "/etc/horde/tls/server.crt", KeyPath: "/etc/horde/tls/server.key", CAPath: "/etc/horde/tls/ca.pem"}
		want := "adopt"
		if invalid {
			c.runtime.Config.Horde.TLS.ServerName = ""
			want = "stable ASCII DNS name"
		}
		if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("invalid=%t: %v", invalid, err)
		}
		if provider.CreateCalls != 0 {
			t.Fatal("failed TLS validation created resources")
		}
	}
}
