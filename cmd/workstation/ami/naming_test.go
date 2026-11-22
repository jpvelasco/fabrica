package ami

import (
	"bytes"
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
	var out bytes.Buffer
	c := buildCommand{
		out: &out,
		cfg: BuildConfig{
			BaseImage:  defaultBaseImage,
			Region:     "us-west-2",
			OutputDir:  t.TempDir(),
			RootDevice: defaultRootDevice,
			NamePrefix: naming.MustResolve("acme"),
		},
		writeFile: os.WriteFile,
		mkdirAll:  os.MkdirAll,
	}
	if err := c.run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out.String(), "Name:       acme-workstation-dcv") {
		t.Errorf("default image name does not follow the prefix:\n%s", out.String())
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
