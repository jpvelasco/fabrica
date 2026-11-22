package ami

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/naming"
)

// The default AMI name follows the configured prefix when --name is unset (#705).
func TestBuildDefaultNameFollowsPrefix(t *testing.T) {
	c := &buildCommand{
		out: io.Discard,
		cfg: BuildConfig{
			Version:    defaultHordeVersion,
			Install:    "docker",
			BaseImage:  defaultBaseImage,
			Region:     defaultRegion,
			OutputDir:  t.TempDir(),
			NamePrefix: naming.MustResolve("acme"),
		},
		writeFile: os.WriteFile,
		mkdirAll:  os.MkdirAll,
	}
	if err := c.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasPrefix(c.cfg.Name, "acme-horde-") {
		t.Errorf("default AMI name = %q, want an acme-horde- prefix", c.cfg.Name)
	}
}

// The gated prefix is refused before the default AMI name is derived from it.
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
