package modstatus

import "testing"

func TestDefaultProbeTCPUnreachable(t *testing.T) {
	if DefaultProbeTCP("127.0.0.1:1") {
		t.Fatal("expected unreachable closed port")
	}
}

func TestProbeHTTPUnreachable(t *testing.T) {
	if ProbeHTTP("127.0.0.1:1", "/health") {
		t.Fatal("expected unreachable endpoint")
	}
}
