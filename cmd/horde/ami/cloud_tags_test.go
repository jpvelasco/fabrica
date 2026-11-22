package ami

import (
	"testing"

	"github.com/jpvelasco/fabrica/cmd/internal/testutil"
	"github.com/jpvelasco/fabrica/internal/tags"
)

// TestRecipeCarriesOperatorTagsAndProtectedOwner checks that cloud.aws.tags
// reach the recipe, the AMI tags, and the Packer tag sets, and that the
// operator cannot override ManagedBy.
func TestRecipeCarriesOperatorTagsAndProtectedOwner(t *testing.T) {
	identity, err := tags.ResolveIdentity("studio")
	if err != nil {
		t.Fatal(err)
	}
	bc := buildCommand{cfg: BuildConfig{
		Identity:      identity,
		CloudTags:     testutil.OperatorTagsFixture(),
		Version:       "5.5.0",
		Install:       "docker",
		BaseImage:     defaultBaseImage,
		Region:        "us-west-2",
		Name:          "fabrica-horde-5-5-0",
		IncludePacker: true,
	}}
	recipe, err := bc.renderTemplate("image-builder.json.tmpl", bc.cfg)
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertRecipeTags(t, recipe, "studio", map[string]string{"Name": "fabrica-horde-5-5-0"})
	packer, err := bc.renderTemplate("packer.hcl.tmpl", bc.cfg)
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertPackerTags(t, packer, "studio")
}
