package ami

import (
	"io"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

// The default image name follows the configured prefix (#705).
func TestDefaultImageNameFollowsPrefix(t *testing.T) {
	if got := defaultImageName(naming.MustResolve("acme")); got != "acme-ddc" {
		t.Fatalf("defaultImageName = %q, want acme-ddc", got)
	}
	if got := defaultImageName(naming.Default); got != "fabrica-ddc" {
		t.Fatalf("defaultImageName(default) = %q, want fabrica-ddc", got)
	}
}

// The gated prefix is refused before the default image name is derived from it.
func TestBuildRejectsGatedNamePrefix(t *testing.T) {
	cfg := &config.Config{}
	cfg.Cloud.AWS.Identity.NamePrefix = "acme"
	source := func() (globals.Runtime, error) { return globals.Runtime{Config: cfg}, nil }
	cmd := newBuildCmd(source, io.Discard)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs([]string{"--output-dir", t.TempDir()})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "#705") {
		t.Fatalf("Execute = %v, want the #705 gate error", err)
	}
}
