package tls

import (
	"bytes"
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/state"
	"github.com/spf13/cobra"
)

func migrationConfig() *config.Config {
	cfg := config.Defaults()
	cfg.Horde.TLS = config.HordeTLSConfig{Enabled: true, ServerName: "horde.example.internal", CertPath: "/etc/horde/tls/server.crt", KeyPath: "/etc/horde/tls/server.key", CAPath: "/etc/horde/tls/ca.pem"}
	return cfg
}

func newTestAdopt(t *testing.T) (adoptCommand, *state.State, *int) {
	t.Helper()
	st := state.NewState("", "")
	st.UpsertModule("horde", "existing-image", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-coordinator"},
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coordinator", Properties: map[string]string{"serviceTokenParameter": "retained"}},
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-agents", Properties: map[string]string{"role": "agent"}},
	})
	writes := 0
	c := adoptCommand{
		runtime: globals.Runtime{Config: migrationConfig()}, options: globals.Options{AssumeYes: true}, out: &bytes.Buffer{},
		read:  func(context.Context) (*state.State, error) { return st, nil },
		write: func(context.Context, *state.State) error { writes++; return nil },
		get: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = []byte(`{"PrivateIpAddress":"10.0.1.2"}`)
			return nil
		},
		runRemote: func(context.Context, string, []string) (cloud.RemoteResult, error) {
			return cloud.RemoteResult{Stdout: `{"verified":true,"contract":1,"port":5000}`}, nil
		},
		verify: func(_ context.Context, c horde.Connection, ip string) error {
			if ip != "10.0.1.2" || !c.TLS {
				t.Fatal("verification target")
			}
			return nil
		},
	}
	return c, st, &writes
}

func TestAdoptSuccessAndRepeat(t *testing.T) {
	c, st, writes := newTestAdopt(t)
	for range 2 {
		if err := c.run(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if *writes != 2 {
		t.Fatal("repeat must reverify/persist")
	}
	m := st.GetModule("horde")
	for _, r := range m.Resources {
		if horde.IsCoordinator(r) && r.Properties[horde.ConnectionProperty] == "" {
			t.Fatal("missing coordinator contract")
		}
		if r.Properties["role"] == "agent" && r.Properties[horde.ConnectionProperty] != "" {
			t.Fatal("agent falsely adopted")
		}
	}
	if m.Resources[1].Properties["serviceTokenParameter"] != "retained" {
		t.Fatal("lost token property")
	}
}

func TestAdoptFailuresDoNotWrite(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*adoptCommand, *state.State)
	}{
		{"read", func(c *adoptCommand, _ *state.State) {
			c.read = func(context.Context) (*state.State, error) { return nil, errors.New("read failed") }
		}},
		{"absent", func(_ *adoptCommand, s *state.State) { s.Modules = nil }},
		{"missing instance", func(_ *adoptCommand, s *state.State) { s.Modules[0].Resources = s.Modules[0].Resources[:1] }},
		{"missing SG", func(_ *adoptCommand, s *state.State) { s.Modules[0].Resources = s.Modules[0].Resources[1:] }},
		{"disabled", func(c *adoptCommand, _ *state.State) { c.runtime.Config.Horde.TLS = config.HordeTLSConfig{} }},
		{"bad config", func(c *adoptCommand, _ *state.State) { c.runtime.Config.Horde.TLS.ServerName = "http://bad" }},
		{"corrupt", func(_ *adoptCommand, s *state.State) {
			s.Modules[0].Resources[1].Properties[horde.ConnectionProperty] = "bad"
		}},
		{"mismatch", func(c *adoptCommand, s *state.State) {
			conn, _ := horde.ConnectionFromConfig(c.runtime.Config.Horde)
			conn.Port = 8443
			conn.GRPCPort = 8443
			s.Modules[0].Resources[1].Properties[horde.ConnectionProperty] = conn.Record()
		}},
		{"no capability", func(c *adoptCommand, _ *state.State) { c.runRemote = nil }},
		{"get", func(c *adoptCommand, _ *state.State) {
			c.get = func(context.Context, *cloud.Resource) error { return errors.New("inspection failure") }
		}},
		{"no IP", func(c *adoptCommand, _ *state.State) {
			c.get = func(context.Context, *cloud.Resource) error { return nil }
		}},
		{"remote", func(c *adoptCommand, _ *state.State) {
			c.runRemote = func(context.Context, string, []string) (cloud.RemoteResult, error) {
				return cloud.RemoteResult{}, errors.New("SECRET_TOKEN=leak")
			}
		}},
		{"remote exit", func(c *adoptCommand, _ *state.State) {
			c.runRemote = func(context.Context, string, []string) (cloud.RemoteResult, error) {
				return cloud.RemoteResult{ExitCode: 1, Stdout: "SECRET_TOKEN=leak", Stderr: "private-key"}, nil
			}
		}},
		{"bad output", func(c *adoptCommand, _ *state.State) {
			c.runRemote = func(context.Context, string, []string) (cloud.RemoteResult, error) {
				return cloud.RemoteResult{Stdout: "SECRET_TOKEN=leak"}, nil
			}
		}},
		{"false verification", func(c *adoptCommand, _ *state.State) {
			c.runRemote = func(context.Context, string, []string) (cloud.RemoteResult, error) {
				return cloud.RemoteResult{Stdout: `{"verified":false}`}, nil
			}
		}},
		{"network", func(c *adoptCommand, _ *state.State) {
			c.verify = func(context.Context, horde.Connection, string) error { return errors.New("certificate mismatch") }
		}},
		{"cancel", func(c *adoptCommand, _ *state.State) {
			c.runRemote = func(ctx context.Context, _ string, _ []string) (cloud.RemoteResult, error) {
				<-ctx.Done()
				return cloud.RemoteResult{}, ctx.Err()
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, st, writes := newTestAdopt(t)
			tc.change(&c, st)
			ctx := context.Background()
			if tc.name == "cancel" {
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			err := c.run(ctx)
			if err == nil || *writes != 0 {
				t.Fatalf("failure/write: %v %d", err, *writes)
			}
			if strings.Contains(err.Error(), "SECRET_TOKEN") || strings.Contains(c.out.(*bytes.Buffer).String(), "private-key") {
				t.Fatal("secret escaped verification")
			}
		})
	}
}

func TestAdoptDryRunDeclineAndWriteFailure(t *testing.T) {
	c, _, writes := newTestAdopt(t)
	c.options.DryRun = true
	c.runRemote = func(context.Context, string, []string) (cloud.RemoteResult, error) {
		t.Fatal("dry-run remote")
		return cloud.RemoteResult{}, nil
	}
	c.get = func(context.Context, *cloud.Resource) error { t.Fatal("dry-run Get"); return nil }
	c.verify = func(context.Context, horde.Connection, string) error { t.Fatal("dry-run HTTPS"); return nil }
	if err := c.run(context.Background()); err != nil || *writes != 0 {
		t.Fatal(err)
	}
	c.options.DryRun = false
	c.options.AssumeYes = false
	c.confirm = func(string) bool { return false }
	if err := c.run(context.Background()); err != nil || *writes != 0 {
		t.Fatal(err)
	}
	c, _, _ = newTestAdopt(t)
	c.write = func(context.Context, *state.State) error { return errors.New("remote write unavailable") }
	if err := c.run(context.Background()); err == nil || !strings.Contains(err.Error(), "persisting") {
		t.Fatal(err)
	}
}

func TestRenderLocalOnly(t *testing.T) {
	dir := t.TempDir()
	configFile := filepath.Join(dir, "fabrica.yaml")
	cfg := `cloud:
  provider: not-a-provider
horde:
  tls:
    enabled: true
    serverName: horde.example.internal
    certPath: /etc/horde/tls/server.crt
    keyPath: /etc/horde/tls/server.key
    caPath: /etc/horde/tls/ca.pem
`
	if err := os.WriteFile(configFile, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(dry bool) error {
		var out bytes.Buffer
		root := &cobra.Command{Use: "fabrica", PersistentPreRunE: func(*cobra.Command, []string) error { t.Fatal("provider initialized"); return nil }}
		root.PersistentFlags().String("config", "", "config")
		root.AddCommand(New(func() (globals.Runtime, error) { t.Fatal("runtime requested"); return globals.Runtime{}, nil }, func() globals.Options { return globals.Options{DryRun: dry} }, &out))
		root.SetArgs([]string{"--config", configFile, "tls", "render", "--output-dir", filepath.Join(dir, "output")})
		return root.Execute()
	}
	if err := run(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "output")); !os.IsNotExist(err) {
		t.Fatal("dry-run wrote files")
	}
	if err := run(false); err != nil {
		t.Fatal(err)
	}
	conn, _ := horde.ConnectionFromConfig(migrationConfig().Horde)
	expected, _ := horde.RuntimeOverlay(conn)
	actual, err := os.ReadFile(filepath.Join(dir, "output", "fabrica-runtime.json"))
	if err != nil || string(actual) != expected {
		t.Fatal("noncanonical output", err)
	}
	if err := run(false); err == nil {
		t.Fatal("overwrite accepted")
	}
}

func TestAdoptCobraWiring(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := migrationConfig()
	provider := &testutil.RemoteCommandProvider{RemoteResult: cloud.RemoteResult{ExitCode: 1, Stdout: "SECRET_TOKEN=leak"}}
	var out bytes.Buffer
	root, options := testutil.BuildTestSubcommand(&out)
	root.AddCommand(New(func() (globals.Runtime, error) { return globals.Runtime{Config: cfg, Provider: provider}, nil }, options, &out))
	st := state.NewState("", "")
	st.UpsertModule("horde", "image", "ready", []state.ModuleResource{{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coordinator"}, {TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-coordinator"}})
	if err := state.WriteStateTo(st, ".fabrica/state.json"); err != nil {
		t.Fatal(err)
	}
	provider.GetResources = map[string]cloud.Resource{cloud.TypeAWSEC2Instance: {Identifier: "i-coordinator", ActualState: []byte(`{"PrivateIpAddress":"127.0.0.1"}`)}}
	root.SetArgs([]string{"tls", "adopt", "--yes"})
	if err := root.Execute(); err == nil || strings.Contains(err.Error(), "SECRET_TOKEN") {
		t.Fatal("real wiring failure", err)
	}
	if provider.RunCommandCalls != 1 {
		t.Fatal("SSM wiring not exercised")
	}
}

func TestAdoptCobraVerifiedHTTPS(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := migrationConfig()
	cfg.Horde.TLS.ServerName = "example.com"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/server/ports" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected probe: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"http":0,"https":` + strconv.Itoa(cfg.Horde.Port) + `,"unencryptedHttp2":0}`))
	}))
	defer server.Close()
	cfg.Horde.Port, _ = strconv.Atoi(strings.TrimPrefix(server.URL, "https://127.0.0.1:"))
	cfg.Horde.ClientCAPath = "ca.pem"
	if err := os.WriteFile("ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	provider := &testutil.RemoteCommandProvider{RemoteResult: cloud.RemoteResult{
		Stdout: `{"verified":true,"contract":1,"port":` + strconv.Itoa(cfg.Horde.Port) + `}`,
	}}
	provider.GetResources = map[string]cloud.Resource{cloud.TypeAWSEC2Instance: {
		Identifier: "i-coordinator", ActualState: []byte(`{"PrivateIpAddress":"127.0.0.1"}`),
	}}
	st := state.NewState("", "")
	st.UpsertModule("horde", "image", "ready", []state.ModuleResource{
		{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coordinator"},
		{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-coordinator"},
	})
	if err := state.WriteStateTo(st, ".fabrica/state.json"); err != nil {
		t.Fatal(err)
	}
	run := func() error {
		var out bytes.Buffer
		root, options := testutil.BuildTestSubcommand(&out)
		root.AddCommand(New(func() (globals.Runtime, error) {
			return globals.Runtime{Config: cfg, ConfigPath: "fabrica.yaml", Provider: provider}, nil
		}, options, &out))
		root.SetArgs([]string{"tls", "adopt", "--yes"})
		return root.Execute()
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	saved, err := state.ReadStateFile(".fabrica/state.json", "", "")
	if err != nil {
		t.Fatal(err)
	}
	connection, err := horde.RecordedConnection(saved.GetModule("horde"))
	if err != nil || connection == nil || !connection.TLS || connection.Port != cfg.Horde.Port {
		t.Fatalf("adoption not persisted: %+v %v", connection, err)
	}
	cfg.Horde.ClientCAPath = "missing.pem"
	if err := run(); err == nil || !strings.Contains(err.Error(), "clientCAPath") {
		t.Fatalf("missing trust did not fail in real wiring: %v", err)
	}
}
