package ami

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/amirecipe"
	"github.com/jpvelasco/fabrica/internal/config"
)

// TestBuildGuideDefaultsFollowConfig runs the cobra command so the region
// comes from cloud.aws.region and the base image defaults to the SSM reference.
func TestBuildGuideDefaultsFollowConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := &config.Config{}
	cfg.Cloud.AWS.Region = "us-west-2"
	cmd := newBuildCmd(func() (globals.Runtime, error) { return globals.Runtime{Config: cfg}, nil }, io.Discard)
	cmd.SetArgs([]string{"--output-dir", "out"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	guide := readGuide(t, "out")
	for _, want := range []string{"in `us-west-2`", "Base AMI: " + amirecipe.DefaultBaseImage} {
		if !strings.Contains(guide, want) {
			t.Errorf("guide missing %q:\n%s", want, guide)
		}
	}
}

// TestBuildFlagsOverrideConfigDefaults checks --region and --base-image still
// win over cloud.aws.region and the SSM default.
func TestBuildFlagsOverrideConfigDefaults(t *testing.T) {
	t.Chdir(t.TempDir())
	cfg := &config.Config{}
	cfg.Cloud.AWS.Region = "us-west-2"
	cmd := newBuildCmd(func() (globals.Runtime, error) { return globals.Runtime{Config: cfg}, nil }, io.Discard)
	cmd.SetArgs([]string{"--output-dir", "out", "--region", "eu-west-1", "--base-image", "ami-0123456789abcdef0"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	guide := readGuide(t, "out")
	if !strings.Contains(guide, "in `eu-west-1`") || !strings.Contains(guide, "Base AMI: ami-0123456789abcdef0") {
		t.Errorf("flags did not override defaults:\n%s", guide)
	}
}

func readGuide(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "build-guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
