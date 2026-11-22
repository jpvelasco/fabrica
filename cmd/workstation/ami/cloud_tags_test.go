package ami

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/tags"
)

// TestBuildCarriesOperatorTagsAndSnapshotStep checks that cloud.aws.tags reach
// the recipe and AMI tags with ManagedBy protected, and that the build guide
// tags the output snapshots so verify-ami.sh passes on a fresh bake.
func TestBuildCarriesOperatorTagsAndSnapshotStep(t *testing.T) {
	identity, err := tags.ResolveIdentity("studio")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	c := buildCommand{
		out:       &bytes.Buffer{},
		cfg:       BuildConfig{Identity: identity, CloudTags: map[string]string{"Project": "studio", "ManagedBy": "foreign"}, BaseImage: defaultBaseImage, Region: "us-west-2", Name: "fabrica-workstation-dcv", OutputDir: dir, RootDevice: defaultRootDevice},
		writeFile: os.WriteFile,
		mkdirAll:  os.MkdirAll,
	}
	if err := c.run(); err != nil {
		t.Fatalf("run: %v", err)
	}

	recipeRaw, err := os.ReadFile(filepath.Join(dir, "image-builder-recipe.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recipe struct {
		Tags    map[string]string `json:"tags"`
		AMITags map[string]string `json:"amiTags"`
	}
	if err := json.Unmarshal(recipeRaw, &recipe); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]map[string]string{"tags": recipe.Tags, "amiTags": recipe.AMITags} {
		if got["Project"] != "studio" || got["ManagedBy"] != "studio" || got["FabricaModule"] != "workstation" {
			t.Errorf("%s = %v; want Project, owner ManagedBy, and FabricaModule", name, got)
		}
	}

	guideRaw, err := os.ReadFile(filepath.Join(dir, "build-guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	guide := string(guideRaw)
	for _, want := range []string{
		"Images[0].BlockDeviceMappings[?Ebs].Ebs.SnapshotId",
		`aws ec2 create-tags --region us-west-2 --resources "$s" --tags '[{"Key":"FabricaModule","Value":"workstation"}`,
		`{"Key":"ManagedBy","Value":"studio"}`,
		`{"Key":"Project","Value":"studio"}`,
	} {
		if !strings.Contains(guide, want) {
			t.Errorf("build guide missing %q", want)
		}
	}
}
