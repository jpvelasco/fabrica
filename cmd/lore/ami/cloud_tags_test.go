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
	b := newRenderTestCommand(true)
	b.cfg.Identity = identity
	b.cfg.CloudTags = testutil.OperatorTagsFixture()
	data, err := b.templateData()
	if err != nil {
		t.Fatal(err)
	}
	recipe, err := b.renderTemplate("image-builder.json.tmpl", data)
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertRecipeTags(t, recipe, "studio", nil)
	packer, err := b.renderTemplate("packer.hcl.tmpl", data)
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertPackerTags(t, packer, "studio")
}
