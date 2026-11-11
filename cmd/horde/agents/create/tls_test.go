package create

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/state"
)

func TestTLSAgentReuseContract(t *testing.T) {
	for _, name := range []string{"new pool", "verified", "legacy", "missing template", "changed CA", "unadopted", "downgrade"} {
		t.Run(name, func(t *testing.T) {
			cfg := config.Defaults()
			cfg.Horde.TLS = config.HordeTLSConfig{Enabled: true, ServerName: "horde.example.internal",
				CertPath: "/etc/horde/tls/server.crt", KeyPath: "/etc/horde/tls/server.key", CAPath: "/etc/horde/tls/ca.pem"}
			cfg.Horde.Agents.CAPath = "/etc/horde/agent-ca.pem"
			connection, err := horde.ConnectionFromConfig(cfg.Horde)
			if err != nil {
				t.Fatal(err)
			}
			resources := []state.ModuleResource{
				{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-agent", Properties: map[string]string{"role": "agent"}},
				{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-agent", Properties: map[string]string{"role": "agent"}},
				{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coordinator", Properties: map[string]string{horde.ConnectionProperty: connection.Record()}},
				{TypeName: cloud.TypeAWSEC2SecurityGroup, Identifier: "sg-coordinator", Properties: map[string]string{horde.ConnectionProperty: connection.Record()}},
			}
			// New pools have no dependent resources; coordinator selection still
			// gets tested with preceding agent records in all reuse cases.
			if name == "new pool" || name == "unadopted" || name == "downgrade" {
				resources = resources[2:]
			} else if name != "missing template" {
				props := map[string]string{"role": "agent", "hordeAgentContract": "1",
					horde.ConnectionProperty: connection.Record(), "hordeAgentCAPath": cfg.Horde.Agents.CAPath}
				if name == "legacy" {
					delete(props, "hordeAgentContract")
				}
				if name == "changed CA" {
					props["hordeAgentCAPath"] = "/etc/old-ca.pem"
				}
				resources = append(resources, state.ModuleResource{TypeName: cloud.TypeAWSEC2LaunchTemplate, Properties: props})
			}
			if name == "unadopted" {
				for i := range resources {
					resources[i].Properties = nil
				}
			}
			if name == "downgrade" {
				cfg.Horde.TLS = config.HordeTLSConfig{}
			}
			st := state.NewState("", "")
			st.UpsertModule("horde", "image", "ready", resources)
			gets := 0
			c := command{runtime: globals.Runtime{Config: cfg}, readState: func() (*state.State, error) { return st, nil },
				getResource: func(_ context.Context, r *cloud.Resource) error {
					gets++
					if strings.Contains(r.Identifier, "agent") {
						t.Fatal("agent mistaken for coordinator")
					}
					if r.TypeName == cloud.TypeAWSEC2Instance {
						r.ActualState = []byte(`{"PrivateIpAddress":"10.0.1.2","SubnetId":"subnet-test"}`)
					} else {
						r.ActualState = []byte(`{"VpcId":"vpc-test"}`)
					}
					return nil
				}}
			ip, port, sg, _, err := c.resolveCoordinator(context.Background())
			if name == "new pool" || name == "verified" {
				if err != nil || ip != "10.0.1.2" || port != connection.Port || sg != "sg-coordinator" || gets != 2 {
					t.Fatalf("resolution: %s %d %s %d %v", ip, port, sg, gets, err)
				}
			} else if err == nil || gets != 0 {
				t.Fatalf("invalid reuse reached AWS: %d %v", gets, err)
			}
		})
	}
}
