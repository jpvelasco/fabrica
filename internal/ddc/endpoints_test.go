package ddc

import (
	"strings"
	"testing"
)

func TestPublicURL(t *testing.T) {
	if PublicURL("10.0.0.1", 80) != "http://10.0.0.1:80" {
		t.Fatal(PublicURL("10.0.0.1", 80))
	}
	if PublicURL("", 80) != "" {
		t.Fatal("empty host")
	}
}

func TestFormatEndpointsYAML(t *testing.T) {
	y := FormatEndpointsYAML(Endpoints{
		Backend: BackendZen, Namespace: "ns", Region: "us-east-1",
		Bucket: "b", PublicURL: "http://10.0.0.1:80", InternalURL: "http://10.0.0.1:8080",
	})
	if !strings.Contains(y, `backend: "zen"`) || !strings.Contains(y, "public_url") {
		t.Fatalf("%s", y)
	}
}
