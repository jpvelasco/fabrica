package status

import (
	"bytes"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
)

func TestProbeReadyUnreachable(t *testing.T) {
	// No listener — probe must return false without panicking.
	if probeReady("127.0.0.1:1") {
		t.Fatal("expected unreachable")
	}
}

func TestRendererNotProvisioned(t *testing.T) {
	var buf bytes.Buffer
	renderer{}.NotProvisioned(&buf, false)
	if buf.Len() == 0 {
		t.Fatal("empty")
	}
	buf.Reset()
	renderer{}.NotProvisioned(&buf, true)
	if buf.Len() == 0 {
		t.Fatal("empty json")
	}
}

func TestRendererResult(t *testing.T) {
	var buf bytes.Buffer
	r := renderer{publicPort: 80, backend: "zen"}
	r.Result(&buf, modstatus.Info{
		ModuleStatus: "ready", InstanceID: "i-1", PrivateIP: "10.0.0.1",
		ProbeAttempted: true, Reachable: true,
	}, false)
	if buf.Len() == 0 {
		t.Fatal("empty")
	}
	buf.Reset()
	r.Result(&buf, modstatus.Info{ModuleStatus: "provisioning", PrivateIP: "10.0.0.1"}, true)
	if buf.Len() == 0 {
		t.Fatal("empty json")
	}
	buf.Reset()
	r.Result(&buf, modstatus.Info{
		ModuleStatus: "ready", ProbeAttempted: true, Reachable: false, PrivateIP: "10.0.0.2",
	}, false)
	if buf.Len() == 0 {
		t.Fatal("empty unreachable")
	}
}
