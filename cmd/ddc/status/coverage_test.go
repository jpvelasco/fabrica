package status

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/ddc"
)

func TestRendererAllBranches(t *testing.T) {
	r := renderer{publicPort: 8081, backend: "scylla"}
	cases := []struct {
		name string
		info modstatus.Info
		json bool
		sub  string
	}{
		{"text_full", modstatus.Info{
			ModuleStatus: "ready", InstanceID: "i-1", InstanceState: "running",
			InstanceType: "m7i.xlarge", PrivateIP: "10.0.0.5", SGID: "sg-1",
			ProbeAttempted: true, Reachable: true,
		}, false, "responding"},
		{"text_unreachable", modstatus.Info{
			ModuleStatus: "ready", PrivateIP: "10.0.0.5", ProbeAttempted: true, Reachable: false,
		}, false, "unreachable"},
		{"text_provisioning", modstatus.Info{ModuleStatus: "provisioning"}, false, "setting up"},
		{"json_responding", modstatus.Info{
			ModuleStatus: "ready", PrivateIP: "10.0.0.5", ProbeAttempted: true, Reachable: true,
		}, true, "responding"},
		{"json_unreachable", modstatus.Info{
			ModuleStatus: "ready", PrivateIP: "10.0.0.5", ProbeAttempted: true, Reachable: false,
		}, true, "unreachable"},
		{"json_setting_up", modstatus.Info{ModuleStatus: "provisioning"}, true, "setting up"},
		{"text_no_ip", modstatus.Info{ModuleStatus: "ready", InstanceID: "i-x"}, false, "Instance ID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			render := r.printText
			if tc.json {
				render = r.printJSON
			}
			render(&buf, tc.info)
			if !strings.Contains(buf.String(), tc.sub) {
				t.Fatalf("want %q in:\n%s", tc.sub, buf.String())
			}
		})
	}
}

func TestNewExecuteNotProvisioned(t *testing.T) {
	t.Chdir(t.TempDir())
	var buf bytes.Buffer
	rt := globals.Runtime{
		Config: &config.Config{DDC: config.DDCConfig{PublicPort: 9090, Backend: ddc.BackendScylla}},
	}
	cmd := New(
		func() (globals.Runtime, error) { return rt, nil },
		func() globals.Options { return globals.Options{} },
		&buf,
	)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "not provisioned") {
		t.Fatalf("%s", buf.String())
	}
}

func TestNewExecuteJSON(t *testing.T) {
	t.Chdir(t.TempDir())
	var buf bytes.Buffer
	cmd := New(
		func() (globals.Runtime, error) {
			return globals.Runtime{Config: &config.Config{}}, nil
		},
		func() globals.Options { return globals.Options{JSONOutput: true} },
		&buf,
	)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "not_provisioned") {
		t.Fatalf("%s", buf.String())
	}
}

func TestNewRuntimeError(t *testing.T) {
	cmd := New(
		func() (globals.Runtime, error) { return globals.Runtime{}, fmt.Errorf("no rt") },
		func() globals.Options { return globals.Options{} },
		&bytes.Buffer{},
	)
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error")
	}
}
