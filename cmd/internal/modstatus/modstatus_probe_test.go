package modstatus

import "testing"

func TestDefaultProbeTCPUnreachable(t *testing.T) {
	if DefaultProbeTCP("127.0.0.1:1") {
		t.Fatal("expected unreachable closed port")
	}
}
