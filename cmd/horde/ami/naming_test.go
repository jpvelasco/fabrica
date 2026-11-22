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
			Region:     "us-west-2",
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

func TestAgentDefaultNameFollowsPrefix(t *testing.T) {
	b := &buildCommand{cfg: BuildConfig{Version: "5.8.4", Role: roleAgent, NamePrefix: naming.MustResolve("acme")}}
	if got := b.defaultName(); got != "acme-horde-agent-5.8.4" {
		t.Fatalf("defaultName() = %q, want acme-horde-agent-5.8.4", got)
	}
}
