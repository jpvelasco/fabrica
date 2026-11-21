package testutil

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/cmd/globals"
	"github.com/jpvelasco/fabrica/internal/amirecipe"
	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/spf13/cobra"
)

// amiRuntimeTestRegion is the cloud.aws.region the runtime tests configure. The
// generated recipes must follow it when --region is not passed.
const amiRuntimeTestRegion = "us-west-2"

// RunAMIRuntimeTests verifies offline AMI generation without a provider, config
// failures before file writes, help without runtime resolution, and that the
// recipe carries cloud.aws.tags, the owner's ManagedBy, the configured region,
// and the default base image unless flags override them.
func RunAMIRuntimeTests(t *testing.T, newCmd func(globals.RuntimeSource, io.Writer) *cobra.Command) {
	t.Helper()
	sourceErr := errors.New("configuration source failed")
	for _, tc := range []struct {
		name, owner, want string
		sourceErr         error
		help              bool
		flags             []string
		wantRegion        string
		wantParent        string
	}{
		{name: "config without provider", wantRegion: amiRuntimeTestRegion, wantParent: amirecipe.DefaultBaseImage},
		{name: "source failure", sourceErr: sourceErr, want: sourceErr.Error()},
		{name: "invalid identity", owner: "bad owner", want: "cloud.aws.identity.managedBy"},
		{name: "custom ownership", owner: "studio-tools", wantRegion: amiRuntimeTestRegion, wantParent: amirecipe.DefaultBaseImage},
		{
			name: "flags override config", flags: []string{"--region", "eu-west-1", "--base-image", "ami-0123456789abcdef0"},
			wantRegion: "eu-west-1", wantParent: "ami-0123456789abcdef0",
		},
		{name: "help without runtime", sourceErr: sourceErr, help: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "output")
			calls := 0
			source := func() (globals.Runtime, error) {
				calls++
				cfg := &config.Config{}
				cfg.Cloud.AWS.Identity.ManagedBy = tc.owner
				cfg.Cloud.AWS.Region = amiRuntimeTestRegion
				cfg.Cloud.AWS.Tags = map[string]string{"ManagedBy": "foreign", "Project": "studio"}
				return globals.Runtime{Config: cfg}, tc.sourceErr
			}
			cmd := newCmd(source, io.Discard)
			cmd.SilenceErrors, cmd.SilenceUsage = true, true
			cmd.SetOut(io.Discard)
			cmd.SetArgs(append([]string{"build", "--output-dir", dir}, tc.flags...))
			if tc.help {
				cmd.SetArgs([]string{"build", "--help"})
			}
			err := cmd.ExecuteContext(t.Context())
			switch {
			case tc.help:
				if err != nil || calls != 0 {
					t.Fatalf("help loaded runtime %d times or failed: %v", calls, err)
				}
			case tc.want != "":
				if err == nil || !strings.Contains(err.Error(), tc.want) || calls != 1 {
					t.Fatalf("runtime calls = %d, error = %v; want one call and %q", calls, err, tc.want)
				}
				if tc.sourceErr != nil && !errors.Is(err, sourceErr) {
					t.Fatalf("source error lost: %v", err)
				}
			default:
				if err != nil || calls != 1 {
					t.Fatalf("generation loaded runtime %d times or failed: %v", calls, err)
				}
			}
			if tc.help || tc.want != "" {
				if _, err := os.Stat(dir); !os.IsNotExist(err) {
					t.Fatalf("help or failure created output directory: %v", err)
				}
				return
			}
			raw, err := fs.ReadFile(os.DirFS(dir), "image-builder-recipe.json")
			if err != nil {
				t.Fatal(err)
			}
			var recipe struct {
				ParentImage string            `json:"parentImage"`
				Tags        map[string]string `json:"tags"`
				AMITags     map[string]string `json:"amiTags"`
				Components  []struct {
					ComponentArn string `json:"componentArn"`
				} `json:"components"`
			}
			if err := json.Unmarshal(raw, &recipe); err != nil {
				t.Fatal(err)
			}
			wantOwner := tc.owner
			if wantOwner == "" {
				wantOwner = "fabrica"
			}
			if recipe.Tags["ManagedBy"] != wantOwner || recipe.AMITags["ManagedBy"] != wantOwner {
				t.Fatalf("ordinary tag selected ownership: tags=%v amiTags=%v", recipe.Tags, recipe.AMITags)
			}
			for name, got := range map[string]map[string]string{"tags": recipe.Tags, "amiTags": recipe.AMITags} {
				if got["Project"] != "studio" {
					t.Fatalf("%s lost cloud.aws.tags: %v", name, got)
				}
			}
			if recipe.ParentImage != tc.wantParent {
				t.Fatalf("parentImage = %q, want %q", recipe.ParentImage, tc.wantParent)
			}
			wantArn := "arn:aws:imagebuilder:" + tc.wantRegion + ":aws:component/update-linux/"
			if len(recipe.Components) == 0 || !strings.HasPrefix(recipe.Components[0].ComponentArn, wantArn) {
				t.Fatalf("components %+v do not use region %s", recipe.Components, tc.wantRegion)
			}
		})
	}
}
