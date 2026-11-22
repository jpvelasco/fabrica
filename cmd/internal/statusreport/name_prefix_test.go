package statusreport

import (
	"context"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
)

func TestCheckBackendRefusesGatedNamePrefix(t *testing.T) {
	cfg := config.Defaults()
	cfg.Cloud.AWS.Identity.NamePrefix = "acme"
	if b := checkBackend(context.Background(), globals.Runtime{Config: cfg}); b.Bucket != "" || b.Table != "" || b.BucketExists != "unknown" {
		t.Fatalf("gated prefix reported backend names: %+v", b)
	}
}
