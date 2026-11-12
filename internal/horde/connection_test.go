package horde

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/state"
)

func tlsConfig() config.HordeConfig {
	return config.HordeConfig{TLS: config.HordeTLSConfig{
		Enabled: true, ServerName: "Horde.Example.internal",
		CertPath: "/etc/horde/tls/server.crt", KeyPath: "/etc/horde/tls/server.key", CAPath: "/etc/horde/tls/ca.pem",
	}}
}

func TestConnection(t *testing.T) {
	legacy, err := ConnectionFromConfig(config.HordeConfig{})
	if err != nil || legacy.Port != 5000 || legacy.GRPCPort != 5002 || legacy.URL("10.0.1.2") != "http://10.0.1.2:5000" || len(legacy.ListenerPorts()) != 2 {
		t.Fatalf("legacy: %+v %v", legacy, err)
	}
	cfg := tlsConfig()
	cfg.Port = 8443
	c, err := ConnectionFromConfig(cfg)
	if err != nil || c.GRPCPort != 8443 || c.URL("") != "https://horde.example.internal:8443" || len(c.ListenerPorts()) != 1 {
		t.Fatalf("TLS: %+v %v", c, err)
	}
	for _, name := range []string{"localhost", "127.0.0.1", "::1", "https://horde.example", "*.example", "host:443", "host.", "bad host", "-bad.example", "höst.example"} {
		t.Run(name, func(t *testing.T) {
			cfg := tlsConfig()
			cfg.TLS.ServerName = name
			if _, err := ConnectionFromConfig(cfg); err == nil {
				t.Fatal("accepted invalid name")
			}
		})
	}
	for _, mutate := range []func(*config.HordeConfig){
		func(c *config.HordeConfig) { c.Port = -1 },
		func(c *config.HordeConfig) { c.Port = 65536 },
		func(c *config.HordeConfig) { c.GRPCPort = 5002 },
		func(c *config.HordeConfig) { c.TLS.Enabled = false },
		func(c *config.HordeConfig) {
			c.TLS.CertPath = "/server.crt"
			c.TLS.KeyPath = "/server.key"
			c.TLS.CAPath = "/ca.pem"
		},
		func(c *config.HordeConfig) { c.TLS.KeyPath = c.TLS.CertPath },
		func(c *config.HordeConfig) { c.TLS.KeyPath = "/other/server.key" },
		func(c *config.HordeConfig) { c.TLS.CertPath = "/etc/horde/../server.crt" },
		func(c *config.HordeConfig) { c.TLS.CertPath = "C:\\certs\\server.crt" },
		func(c *config.HordeConfig) { c.TLS.CertPath = "/etc/horde/tls/" },
		func(c *config.HordeConfig) { c.Agents.CAPath = "relative.pem" },
		func(c *config.HordeConfig) { c.TLS.CAPath = "" },
		func(c *config.HordeConfig) { c.TLS.KeyPath = "/etc/horde/tls/control\u001f.key" },
	} {
		cfg := tlsConfig()
		mutate(&cfg)
		if _, err := ConnectionFromConfig(cfg); err == nil {
			t.Fatalf("accepted %+v", cfg)
		}
	}
}

func TestConnectionState(t *testing.T) {
	cfg := tlsConfig()
	c, _ := ConnectionFromConfig(cfg)
	record := c.Record()
	module := &state.ModuleState{Resources: []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Properties: map[string]string{ConnectionProperty: record}},
		{TypeName: cloud.TypeAWSEC2Instance, Properties: map[string]string{ConnectionProperty: record}},
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Properties: map[string]string{"role": "agent", ConnectionProperty: "bad"}},
	}}
	if got, err := ResolveConnection(cfg, module); err != nil || got != c {
		t.Fatalf("%+v %v", got, err)
	}
	if r, ok := CoordinatorResource(module, cloud.TypeAWSEC2SecurityGroup); !ok || r.Properties["role"] == "agent" {
		t.Fatal("coordinator selection")
	}
	if _, ok := CoordinatorResource(nil, cloud.TypeAWSEC2Instance); ok {
		t.Fatal("nil coordinator")
	}
	if _, ok := CoordinatorResource(module, "missing"); ok {
		t.Fatal("unknown resource selected")
	}
	if _, err := ResolveConnection(config.HordeConfig{}, module); err == nil {
		t.Fatal("downgrade accepted")
	}
	if _, err := ResolveConnection(cfg, &state.ModuleState{}); err == nil || !strings.Contains(err.Error(), "adopt") {
		t.Fatal(err)
	}
	if _, err := ResolveConnection(config.HordeConfig{}, &state.ModuleState{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveConnection(cfg, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordedConnection(nil); err != nil {
		t.Fatal(err)
	}
	invalid := cfg
	invalid.Port = -1
	if _, err := ResolveConnection(invalid, module); err == nil {
		t.Fatal("invalid configured port accepted against state")
	}
	for _, bad := range []string{"{", `{"version":2}`, `{"version":0}`, `{"version":1,"port":5000,"grpcPort":5002,"tls":true}`, `{}`, strings.Replace(record, `"port":5000`, `"port":5001`, 1)} {
		module.Resources[1].Properties[ConnectionProperty] = bad
		if _, err := ResolveConnection(cfg, module); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	module.Resources[1].Properties = nil
	if _, err := ResolveConnection(cfg, module); err != nil {
		t.Fatalf("partial SG record: %v", err)
	}
}

func TestDependentContract(t *testing.T) {
	c, _ := ConnectionFromConfig(tlsConfig())
	for _, properties := range []map[string]string{
		nil,
		{"contract": "1"},
		{"contract": "0", ConnectionProperty: c.Record()},
		{"contract": "1", ConnectionProperty: "invalid"},
	} {
		if err := ValidateDependent(c, state.ModuleResource{Properties: properties}, "contract"); err == nil {
			t.Fatal("unverified TLS dependent accepted")
		}
	}
	verified := state.ModuleResource{Properties: map[string]string{"contract": "1", ConnectionProperty: c.Record()}}
	if err := ValidateDependent(c, verified, "contract"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateDependent(Connection{}, state.ModuleResource{}, "contract"); err != nil {
		t.Fatal("legacy HTTP dependent rejected", err)
	}
}
