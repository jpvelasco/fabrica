package ami

import (
	"io"
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

// A custom prefix flows through the build command into the rendered output.
func TestBuildAcceptsCustomNamePrefix(t *testing.T) {
	cfg := &config.Config{}
	cfg.Cloud.AWS.Identity.NamePrefix = "acme"
	source := func() (globals.Runtime, error) { return globals.Runtime{Config: cfg}, nil }
	cmd := newBuildCmd(source, io.Discard)
	cmd.SilenceErrors, cmd.SilenceUsage = true, true
	cmd.SetArgs([]string{"--output-dir", t.TempDir()})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}
