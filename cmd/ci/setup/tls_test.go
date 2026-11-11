package setup

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/jpvelasco/fabrica/internal/ci"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/state"
)

func TestTLSSetupProjectContracts(t *testing.T) {
	for _, mode := range []string{"new", "reuse", "legacy", "remote existing"} {
		t.Run(mode, func(t *testing.T) {
			var out bytes.Buffer
			c, _ := newCmd(&out, nil, true)
			c.runtime.Config.Horde.TLS = config.HordeTLSConfig{Enabled: true, ServerName: "horde.example.internal", CertPath: "/etc/horde/tls/server.crt", KeyPath: "/etc/horde/tls/server.key", CAPath: "/etc/horde/tls/ca.pem"}
			conn, _ := horde.ConnectionFromConfig(c.runtime.Config.Horde)
			st := state.NewState("", "")
			st.UpsertModule("horde", "image", "ready", []state.ModuleResource{{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coordinator", Properties: map[string]string{horde.ConnectionProperty: conn.Record()}}})
			if mode == "reuse" || mode == "legacy" {
				props := map[string]string{"retained": "yes"}
				if mode == "reuse" {
					props[horde.ConnectionProperty] = conn.Record()
					props["hordeCIContract"] = "1"
				}
				st.UpsertModule("ci", "fabrica-ci", "ready", []state.ModuleResource{{TypeName: ci.TypeAWSCodeBuildProject, Identifier: "fabrica-ci", Properties: props}})
			}
			c.readState = func() (*state.State, error) { return st, nil }
			c.writeState = func(*state.State) error { return nil }
			c.ensureProject = func(_ context.Context, spec cloud.CodeBuildProjectSpec) (bool, error) {
				if spec.EnvDefaults["HORDE_URL"] != conn.URL("") || spec.Buildspec == "" {
					t.Fatal("project transport")
				}
				return mode == "new", nil
			}
			err := c.run(context.Background())
			if mode == "legacy" || mode == "remote existing" {
				if err == nil {
					t.Fatal("unsupported reuse accepted")
				}
				if project, ok := st.GetModuleResource("ci", ci.TypeAWSCodeBuildProject); ok && project.Properties["hordeCIContract"] == "1" {
					t.Fatal("legacy project falsely upgraded")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			project, _ := st.GetModuleResource("ci", ci.TypeAWSCodeBuildProject)
			if project.Properties[horde.ConnectionProperty] != conn.Record() || project.Properties["hordeCIContract"] != "1" {
				t.Fatal("missing project contract")
			}
			if mode == "reuse" && project.Properties["retained"] != "yes" {
				t.Fatal("lost existing properties")
			}
		})
	}
}

func TestTLSSetupRejectsUnverifiedCoordinatorBeforeCreation(t *testing.T) {
	for _, mode := range []string{"read failure", "invalid config", "missing coordinator", "unadopted coordinator"} {
		t.Run(mode, func(t *testing.T) {
			c, created := newCmd(&bytes.Buffer{}, nil, true)
			c.runtime.Config.Horde.TLS = config.HordeTLSConfig{Enabled: true, ServerName: "horde.example.internal",
				CertPath: "/etc/horde/tls/server.crt", KeyPath: "/etc/horde/tls/server.key", CAPath: "/etc/horde/tls/ca.pem"}
			st := state.NewState("", "")
			if mode == "unadopted coordinator" {
				st.UpsertModule("horde", "image", "ready", []state.ModuleResource{{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coordinator"}})
			}
			if mode == "invalid config" {
				c.runtime.Config.Horde.TLS.ServerName = "https://wrong"
			}
			c.readState = func() (*state.State, error) {
				if mode == "read failure" {
					return nil, errors.New("remote state unavailable")
				}
				return st, nil
			}
			if err := c.run(context.Background()); err == nil || len(*created) != 0 {
				t.Fatalf("unverified setup mutated resources: %v %v", *created, err)
			}
		})
	}
}
