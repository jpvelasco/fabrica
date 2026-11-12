package horde

import (
	"context"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/cloud"
)

func TestCreateRejectsTLSBeforeResolvingVPC(t *testing.T) {
	cfg := tlsConfig()
	cfg.AmiID = "ami-fixture"
	resolver := &cloud.TestVPCResolver{}
	plan, err := NewCreatePlan(context.Background(), cfg, "", "", resolver, nil)
	if err == nil || !strings.Contains(err.Error(), "TLS provisioning is not enabled") {
		t.Fatalf("incomplete TLS creation accepted: %v", err)
	}
	if plan != nil || resolver.Calls != 0 {
		t.Fatal("TLS guard allowed plan or VPC work")
	}
	cfg.Port = -1
	if _, err := NewCreatePlan(context.Background(), cfg, "", "", resolver, nil); err == nil {
		t.Fatal("invalid connection accepted")
	}
	if resolver.Calls != 0 {
		t.Fatal("invalid connection reached VPC resolution")
	}
}
