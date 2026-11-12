package status

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/horde"
	"github.com/jpvelasco/fabrica/internal/state"
)

func TestTLSResolutionAndRendering(t *testing.T) {
	cfg := config.Defaults()
	cfg.Horde.TLS = config.HordeTLSConfig{Enabled: true, ServerName: "horde.example.internal", CertPath: "/etc/horde/tls/cert.pem", KeyPath: "/etc/horde/tls/key.pem", CAPath: "/etc/horde/tls/ca.pem"}
	conn, _ := horde.ConnectionFromConfig(cfg.Horde)
	m := &state.ModuleState{Resources: []state.ModuleResource{{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coordinator", Properties: map[string]string{horde.ConnectionProperty: conn.Record()}}}}
	spec, err := resolveState(globals.Runtime{Config: cfg}, context.Background(), m)
	if err != nil || spec.ProbeContext == nil || spec.Ready == nil {
		t.Fatal("resolution", err)
	}
	var out bytes.Buffer
	spec.Renderer.Result(&out, modstatus.Info{PrivateIP: "10.0.1.2", ProbeError: "untrusted certificate"}, false)
	if !strings.Contains(out.String(), "https://horde.example.internal:5000") || !strings.Contains(out.String(), "untrusted certificate") {
		t.Fatal(out.String())
	}
	out.Reset()
	spec.Renderer.Result(&out, modstatus.Info{PrivateIP: "10.0.1.2"}, true)
	if strings.Contains(out.String(), "http://") {
		t.Fatal("downgraded renderer")
	}
	cfg.Horde.ClientCAPath = "missing.pem"
	if _, err := resolveState(globals.Runtime{Config: cfg}, context.Background(), m); err == nil {
		t.Fatal("missing CA accepted")
	}
	cfg.Horde.TLS = config.HordeTLSConfig{}
	if _, err := resolveState(globals.Runtime{Config: cfg}, context.Background(), m); err == nil {
		t.Fatal("state downgrade accepted")
	}
}

func TestReadyMergesCurrentStateAndRejectsChangedInstance(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := config.Defaults()
	rt := globals.Runtime{Config: cfg}
	observed := &state.ModuleState{Name: "horde", Status: "provisioning", Resources: []state.ModuleResource{{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-original"}}}
	spec, err := resolveState(rt, context.Background(), observed)
	if err != nil {
		t.Fatal(err)
	}
	current := state.NewState("", "")
	current.UpsertModule("horde", "image", "provisioning", append(observed.Resources, state.ModuleResource{TypeName: "new-resource", Identifier: "concurrent-update"}))
	current.UpsertModule("ci", "project", "ready", nil)
	if err := state.WriteStateTo(current, ".fabrica/state.json"); err != nil {
		t.Fatal(err)
	}
	if err := spec.Ready(context.Background(), observed); err != nil {
		t.Fatal(err)
	}
	got, err := state.ReadStateFile(".fabrica/state.json", "", "")
	if err != nil || len(got.GetModule("horde").Resources) != 2 || got.GetModule("ci") == nil || got.GetModule("horde").Status != "ready" {
		t.Fatal("lost concurrent state", err)
	}
	got.GetModule("horde").Resources[0].Identifier = "i-replacement"
	if err := state.WriteStateTo(got, ".fabrica/state.json"); err != nil {
		t.Fatal(err)
	}
	if err := spec.Ready(context.Background(), observed); err == nil {
		t.Fatal("changed instance promoted")
	}
}

func TestContextProbeFailureCannotPromote(t *testing.T) {
	st := state.NewState("", "")
	st.UpsertModule("horde", "image", "provisioning", []state.ModuleResource{{TypeName: cloud.TypeAWSEC2Instance, Identifier: "i-coordinator"}})
	var out bytes.Buffer
	written := false
	c := modstatus.Command{
		Spec: modstatus.Spec{ModuleName: "horde", ProbePort: 5000}, Out: &out,
		Renderer:   modstatus.NewRenderer("Horde", "create", renderer{port: 5000, grpcPort: 5000}.printText, renderer{}.printJSON),
		ReadState:  func(context.Context) (*state.State, error) { return st, nil },
		WriteState: func(context.Context, *state.State) error { written = true; return nil },
		GetResource: func(_ context.Context, r *cloud.Resource) error {
			r.ActualState = []byte(`{"PrivateIpAddress":"10.0.1.2"}`)
			return nil
		},
		ProbeContext: func(context.Context, string) error { return errors.New("certificate SAN mismatch") },
	}
	if err := c.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if written || !strings.Contains(out.String(), "certificate SAN mismatch") {
		t.Fatal("TLS failure lost", out.String())
	}
}
