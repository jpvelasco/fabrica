package status

import (
	"bytes"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
)

func TestRendererResult(t *testing.T) {
	var buf bytes.Buffer
	r := renderer{publicPort: 80, backend: "zen"}
	r.printText(&buf, modstatus.Info{
		ModuleStatus: "ready", InstanceID: "i-1", PrivateIP: "10.0.0.1",
		ProbeAttempted: true, Reachable: true,
	})
	if buf.Len() == 0 {
		t.Fatal("empty")
	}
	buf.Reset()
	r.printJSON(&buf, modstatus.Info{ModuleStatus: "provisioning", PrivateIP: "10.0.0.1"})
	if buf.Len() == 0 {
		t.Fatal("empty json")
	}
	buf.Reset()
	r.printText(&buf, modstatus.Info{
		ModuleStatus: "ready", ProbeAttempted: true, Reachable: false, PrivateIP: "10.0.0.2",
	})
	if buf.Len() == 0 {
		t.Fatal("empty unreachable")
	}
}
