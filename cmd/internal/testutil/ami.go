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
// recipe and AMI tags carry cloud.aws.tags with the owner's ManagedBy, the
// configured region, and the default base image unless flags override them.
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
				cfg.Cloud.AWS.Tags = OperatorTagsFixture()
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
			wantOwner := tc.owner
			if wantOwner == "" {
				wantOwner = "fabrica"
			}
			AssertRecipeTags(t, raw, wantOwner, nil)
			var recipe struct {
				ParentImage string `json:"parentImage"`
				Components  []struct {
					ComponentArn string `json:"componentArn"`
				} `json:"components"`
			}
			if err := json.Unmarshal(raw, &recipe); err != nil {
				t.Fatal(err)
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

// OperatorTagsFixture is the cloud.aws.tags seed the AMI tag tests use: an
// operator tag plus a ManagedBy that must lose to the resolved owner.
func OperatorTagsFixture() map[string]string {
	return map[string]string{"Project": "studio", "ManagedBy": "foreign"}
}

// AssertRecipeTags checks that an Image Builder recipe's tags and amiTags both
// carry the operator tag, the protected owner, and every extra want entry.
func AssertRecipeTags(t *testing.T, recipe []byte, owner string, want map[string]string) {
	t.Helper()
	var doc struct {
		Tags    map[string]string `json:"tags"`
		AMITags map[string]string `json:"amiTags"`
	}
	if err := json.Unmarshal(recipe, &doc); err != nil {
		t.Fatalf("recipe is not JSON: %v", err)
	}
	expect := map[string]string{"Project": "studio", "ManagedBy": owner}
	for k, v := range want {
		expect[k] = v
	}
	for name, got := range map[string]map[string]string{"tags": doc.Tags, "amiTags": doc.AMITags} {
		for k, v := range expect {
			if got[k] != v {
				t.Errorf("recipe %s[%s] = %q, want %q (all: %v)", name, k, got[k], v, got)
			}
		}
	}
}

// AssertPackerTags checks that rendered Packer HCL carries the operator tag and
// the protected owner in its tag sets.
func AssertPackerTags(t *testing.T, packer []byte, owner string) {
	t.Helper()
	for _, want := range []string{`"Project" = "studio"`, `"ManagedBy" = "` + owner + `"`} {
		if !strings.Contains(string(packer), want) {
			t.Errorf("packer tags missing %s:\n%s", want, packer)
		}
	}
}
