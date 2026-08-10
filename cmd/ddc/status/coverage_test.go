package status

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/internal/cloud"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/ddc"
	fabricastate "github.com/jpvelasco/fabrica/internal/state"
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

// stateWithEdges returns a readState seam backed by a DDC state that also has
// one edge region (eu-west-1) plus the home SG.
func stateWithEdges() func() (*fabricastate.State, error) {
	st := fabricastate.NewState("123456789012", "us-east-1")
	st.UpsertModule("ddc", "ami-ddc", "ready", []fabricastate.ModuleResource{
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-home"},
		{TypeName: "AWS::EC2::Instance", Identifier: "i-home"},
		{TypeName: "AWS::EC2::SecurityGroup", Identifier: "sg-edge", Properties: map[string]string{"region": "eu-west-1", "role": ddc.RoleEdge}},
		{TypeName: "AWS::EC2::Instance", Identifier: "i-edge", Properties: map[string]string{"region": "eu-west-1", "role": ddc.RoleEdge, "instanceType": "m7i.large"}},
	})
	return func() (*fabricastate.State, error) { return st, nil }
}

func TestRendererEdgeText(t *testing.T) {
	r := renderer{publicPort: 8081, backend: "zen", readState: stateWithEdges()}
	var buf bytes.Buffer
	r.printText(&buf, modstatus.Info{ModuleStatus: "ready", SGID: "sg-home"})
	out := buf.String()
	for _, want := range []string{"eu-west-1", "i-edge", "sg-edge", "Edge regions:  1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRendererEdgeJSON(t *testing.T) {
	r := renderer{publicPort: 8081, backend: "zen", readState: stateWithEdges()}
	var buf bytes.Buffer
	r.printJSON(&buf, modstatus.Info{ModuleStatus: "ready", SGID: "sg-home", InstanceID: "i-home"})
	out := buf.String()
	for _, want := range []string{`"edges"`, "eu-west-1", `"instanceId": "i-edge"`, `"sgId": "sg-edge"`, `"status": "provisioned"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"sgId": "sg-edge"`) {
		// the home SG (sg-home) must remain the top-level sgId
		if !strings.Contains(out, `"sgId": "sg-home"`) {
			t.Fatalf("home sgId lost:\n%s", out)
		}
	}
}

func TestRendererEdgeReadStateError(t *testing.T) {
	r := renderer{
		publicPort: 8081, backend: "zen",
		readState: func() (*fabricastate.State, error) { return nil, fmt.Errorf("state missing") },
	}
	var buf bytes.Buffer
	r.printText(&buf, modstatus.Info{ModuleStatus: "ready", SGID: "sg-home"})
	out := buf.String()
	if strings.Contains(out, "Edge regions:  1") {
		t.Fatalf("unexpected edge list on read error:\n%s", out)
	}
	if !strings.Contains(out, "none") {
		t.Fatalf("expected no-edges fallback:\n%s", out)
	}
}

func TestEdgeCompositeStatus(t *testing.T) {
	cases := []struct {
		name   string
		status ddc.EdgeStatus
		want   string
	}{
		{"ready", ddc.EdgeStatus{ProbeStatus: "ready"}, "ready"},
		{"unreachable", ddc.EdgeStatus{ProbeStatus: "unreachable"}, "unreachable"},
		{"skipped_running", ddc.EdgeStatus{ProbeStatus: "skipped", InstanceState: "running"}, "running"},
		{"skipped_stopped", ddc.EdgeStatus{ProbeStatus: "skipped", InstanceState: "stopped"}, "stopped"},
		{"skipped_terminated", ddc.EdgeStatus{ProbeStatus: "skipped", InstanceState: "terminated"}, "terminated"},
		{"skipped_missing", ddc.EdgeStatus{ProbeStatus: "skipped", InstanceState: "missing"}, "missing"},
		{"skipped_empty", ddc.EdgeStatus{ProbeStatus: "skipped"}, "provisioned"},
		{"empty", ddc.EdgeStatus{}, "provisioned"},
		{"running_no_probe", ddc.EdgeStatus{InstanceState: "running"}, "running"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := edgeCompositeStatus(tc.status)
			if got != tc.want {
				t.Fatalf("edgeCompositeStatus(%+v) = %q, want %q", tc.status, got, tc.want)
			}
		})
	}
}

func TestRendererEdgeLiveProbe(t *testing.T) {
	// Test with a fake getEdgeStatus seam that returns live data.
	r := renderer{
		publicPort: 80, backend: "zen",
		readState: stateWithEdges(),
		getEdgeStatus: func(ctx context.Context, edges []ddc.EdgeResource, provider cloud.Provider) []ddc.EdgeStatus {
			return []ddc.EdgeStatus{
				{
					Region:        "eu-west-1",
					InstanceID:    "i-edge",
					InstanceState: "running",
					InstanceType:  "m7i.large",
					PrivateIP:     "10.0.2.1",
					ProbeStatus:   "ready",
				},
			}
		},
	}
	var buf bytes.Buffer
	r.printText(&buf, modstatus.Info{ModuleStatus: "ready", SGID: "sg-home"})
	out := buf.String()
	for _, want := range []string{"eu-west-1", "running", "ready", "10.0.2.1", "Instance state: running", "Health:        ready"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRendererEdgeLiveProbeJSON(t *testing.T) {
	r := renderer{
		publicPort: 80, backend: "zen",
		readState: stateWithEdges(),
		getEdgeStatus: func(ctx context.Context, edges []ddc.EdgeResource, provider cloud.Provider) []ddc.EdgeStatus {
			return []ddc.EdgeStatus{
				{
					Region:        "eu-west-1",
					InstanceID:    "i-edge",
					InstanceState: "running",
					InstanceType:  "m7i.large",
					PrivateIP:     "10.0.2.1",
					ProbeStatus:   "ready",
				},
			}
		},
	}
	var buf bytes.Buffer
	r.printJSON(&buf, modstatus.Info{ModuleStatus: "ready", SGID: "sg-home"})
	out := buf.String()
	for _, want := range []string{`"region": "eu-west-1"`, `"instanceState": "running"`, `"privateIp": "10.0.2.1"`, `"probeStatus": "ready"`, `"status": "ready"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRendererEdgeUnreachable(t *testing.T) {
	r := renderer{
		publicPort: 80, backend: "zen",
		readState: stateWithEdges(),
		getEdgeStatus: func(ctx context.Context, edges []ddc.EdgeResource, provider cloud.Provider) []ddc.EdgeStatus {
			return []ddc.EdgeStatus{
				{
					Region:        "eu-west-1",
					InstanceID:    "i-edge",
					InstanceState: "running",
					PrivateIP:     "10.0.2.1",
					ProbeStatus:   "unreachable",
					ProbeError:    "connection timed out",
				},
			}
		},
	}
	var buf bytes.Buffer
	r.printText(&buf, modstatus.Info{ModuleStatus: "ready", SGID: "sg-home"})
	out := buf.String()
	for _, want := range []string{"unreachable", "connection timed out", "Probe error"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRendererEdgeMissing(t *testing.T) {
	r := renderer{
		publicPort: 80, backend: "zen",
		readState: stateWithEdges(),
		getEdgeStatus: func(ctx context.Context, edges []ddc.EdgeResource, provider cloud.Provider) []ddc.EdgeStatus {
			return []ddc.EdgeStatus{
				{
					Region:        "eu-west-1",
					InstanceID:    "i-edge",
					InstanceState: "missing",
					ProbeStatus:   "skipped",
					ProbeError:    "Cloud Control Get failed: resource not found",
				},
			}
		},
	}
	var buf bytes.Buffer
	r.printText(&buf, modstatus.Info{ModuleStatus: "ready", SGID: "sg-home"})
	out := buf.String()
	for _, want := range []string{"Instance state: missing", "Cloud Control Get failed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRendererEdgeNoProvider(t *testing.T) {
	// When provider is nil, edges should fall back to state-only "provisioned".
	r := renderer{
		publicPort: 80, backend: "zen",
		readState: stateWithEdges(),
		provider:  nil,
		getEdgeStatus: func(ctx context.Context, edges []ddc.EdgeResource, provider cloud.Provider) []ddc.EdgeStatus {
			return nil
		},
	}
	var buf bytes.Buffer
	r.printJSON(&buf, modstatus.Info{ModuleStatus: "ready", SGID: "sg-home"})
	out := buf.String()
	// Should show "provisioned" status since no live data.
	if !strings.Contains(out, `"status": "provisioned"`) {
		t.Fatalf("expected provisioned fallback:\n%s", out)
	}
}

func TestRendererEdgeNoSeam(t *testing.T) {
	// When getEdgeStatus seam is nil, edges should fall back to state-only.
	r := renderer{
		publicPort: 80, backend: "zen",
		readState:     stateWithEdges(),
		provider:      nil, // no provider
		getEdgeStatus: nil, // seam not set
	}
	var buf bytes.Buffer
	r.printJSON(&buf, modstatus.Info{ModuleStatus: "ready", SGID: "sg-home"})
	out := buf.String()
	if !strings.Contains(out, `"status": "provisioned"`) {
		t.Fatalf("expected provisioned fallback when seam is nil:\n%s", out)
	}
}
