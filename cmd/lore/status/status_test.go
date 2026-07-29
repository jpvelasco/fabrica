package status

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/internal/lore"
)

func TestRendererResultText(t *testing.T) {
	var out bytes.Buffer
	r := renderer{grpcPort: lore.DefaultGRPCPort, httpPort: lore.DefaultHTTPPort}
	r.printText(&out, modstatus.Info{
		ModuleStatus:   "ready",
		InstanceID:     "i-abc",
		InstanceType:   "m5.xlarge",
		PrivateIP:      "10.0.0.5",
		SGID:           "sg-1",
		ProbeAttempted: true,
		Reachable:      true,
	})
	got := out.String()
	for _, want := range []string{"Lore loreserver", "ready", "10.0.0.5", "41339", "41337", "responding"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestRendererResultJSON(t *testing.T) {
	var out bytes.Buffer
	r := renderer{grpcPort: lore.DefaultGRPCPort, httpPort: lore.DefaultHTTPPort}
	r.printJSON(&out, modstatus.Info{
		ModuleStatus:   "ready",
		PrivateIP:      "10.0.0.5",
		ProbeAttempted: true,
		Reachable:      true,
	})
	got := out.String()
	for _, want := range []string{`"loreUrl"`, `"loreGrpc"`, `"responding"`, "10.0.0.5"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}
