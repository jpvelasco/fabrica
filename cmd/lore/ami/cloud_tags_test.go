package ami

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/amirecipe"
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
	b.cfg.CloudTags = map[string]string{"Project": "studio", "ManagedBy": "foreign"}
	data, err := b.templateData()
	if err != nil {
		t.Fatal(err)
	}
	recipe, err := b.renderTemplate("image-builder.json.tmpl", data)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Tags    map[string]string `json:"tags"`
		AMITags map[string]string `json:"amiTags"`
	}
	if err := json.Unmarshal(recipe, &doc); err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]map[string]string{"tags": doc.Tags, "amiTags": doc.AMITags} {
		if got["Project"] != "studio" || got["ManagedBy"] != "studio" {
			t.Errorf("%s = %v; want Project=studio and ManagedBy=studio", name, got)
		}
	}
	packer, err := b.renderTemplate("packer.hcl.tmpl", data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(packer), `"Project" = "studio"`) || !strings.Contains(string(packer), `"ManagedBy" = "studio"`) {
		t.Errorf("packer common_tags missing operator or owner tags:\n%s", packer)
	}
}

// TestExplicitBaseImagePinsPackerSource checks --base-image ami-… still renders
// the literal AMI in both the recipe and the Packer source.
func TestExplicitBaseImagePinsPackerSource(t *testing.T) {
	b := newRenderTestCommand(true)
	b.cfg.BaseImage = "ami-0123456789abcdef0"
	data, err := b.templateData()
	if err != nil {
		t.Fatal(err)
	}
	recipe, err := b.renderTemplate("image-builder.json.tmpl", data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(recipe), `"parentImage": "ami-0123456789abcdef0"`) {
		t.Errorf("recipe parentImage should be the explicit AMI:\n%s", recipe)
	}
	packer, err := b.renderTemplate("packer.hcl.tmpl", data)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(packer), `source_ami    = "ami-0123456789abcdef0"`) || strings.Contains(string(packer), "amazon-parameterstore") {
		t.Errorf("explicit AMI should pin the Packer source with no data source:\n%s", packer)
	}
	if amirecipe.PackerSource(b.cfg.BaseImage) != `source_ami    = "ami-0123456789abcdef0"` {
		t.Fatal("PackerSource disagrees with the template")
	}
}
