package status

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/modstatus"
	"github.com/jpvelasco/fabrica/internal/lore"
)

func TestRendererNotProvisioned(t *testing.T) {
	var out bytes.Buffer
	renderer{}.NotProvisioned(&out, false)
	if !strings.Contains(out.String(), "not provisioned") {
		t.Fatalf("got %q", out.String())
	}
}

func TestRendererNotProvisionedJSON(t *testing.T) {
	var out bytes.Buffer
	renderer{}.NotProvisioned(&out, true)
	if !strings.Contains(out.String(), "not_provisioned") {
		t.Fatalf("got %q", out.String())
	}
}

func TestRendererResultText(t *testing.T) {
	var out bytes.Buffer
	r := renderer{grpcPort: lore.DefaultGRPCPort, httpPort: lore.DefaultHTTPPort}
	r.Result(&out, modstatus.Info{
		ModuleStatus:   "ready",
		InstanceID:     "i-abc",
		InstanceType:   "m5.xlarge",
		PrivateIP:      "10.0.0.5",
		SGID:           "sg-1",
		ProbeAttempted: true,
		Reachable:      true,
	}, false)
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
	r.Result(&out, modstatus.Info{
		ModuleStatus:   "ready",
		PrivateIP:      "10.0.0.5",
		ProbeAttempted: true,
		Reachable:      true,
	}, true)
	got := out.String()
	for _, want := range []string{`"loreUrl"`, `"loreGrpc"`, `"responding"`, "10.0.0.5"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}

func TestProbeHealthCheckOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health_check" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	// httptest URL is http://127.0.0.1:port — strip scheme for address form host:port
	addr := strings.TrimPrefix(srv.URL, "http://")
	if !probeHealthCheck(addr) {
		t.Fatal("expected reachable")
	}
}

func TestProbeHealthCheckFail(t *testing.T) {
	if probeHealthCheck("127.0.0.1:1") {
		t.Fatal("expected unreachable")
	}
}
