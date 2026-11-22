package doctorchecks

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

func TestNamePrefixCheckWarnsOnUnpinnedCustomPrefix(t *testing.T) {
	acme := naming.MustResolve("acme")
	cfg := config.Defaults()

	got := namePrefixCheck(acme, cfg)
	if got.Status != "warning" || !strings.Contains(got.Message, "acme") || !strings.Contains(got.Message, "unpinned") {
		t.Fatalf("unpinned acme check = %+v", got)
	}

	cfg.State.Bucket = "acme-state-123456789012"
	if got := namePrefixCheck(acme, cfg); got.Status != "warning" {
		t.Fatalf("half-pinned acme check = %+v", got)
	}

	cfg.State.Table = "acme-state-lock"
	if got := namePrefixCheck(acme, cfg); got.Status != "ok" {
		t.Fatalf("pinned acme check = %+v", got)
	}
}

func TestNamePrefixCheckDefaultIsOK(t *testing.T) {
	if got := namePrefixCheck(naming.Default, config.Defaults()); got.Status != "ok" || got.Message != "fabrica" {
		t.Fatalf("default check = %+v", got)
	}
}

func TestCheckNamePrefixReportsGatedPrefixAsFailure(t *testing.T) {
	cfg := config.Defaults()
	cfg.Cloud.AWS.Identity.NamePrefix = "acme"
	if got := checkNamePrefix(globals.Runtime{Config: cfg}); got.Status != "fail" {
		t.Fatalf("gated prefix check = %+v", got)
	}
}
