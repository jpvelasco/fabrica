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
	bc := buildCommand{cfg: BuildConfig{
		Identity:      identity,
		CloudTags:     map[string]string{"Project": "studio", "ManagedBy": "foreign"},
		Version:       "5.5.0",
		Install:       "docker",
		BaseImage:     amirecipe.DefaultBaseImage,
		Region:        "us-west-2",
		Name:          "fabrica-horde-5-5-0",
		IncludePacker: true,
	}}
	recipe, err := bc.renderTemplate("image-builder.json.tmpl", bc.cfg)
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
		if got["Project"] != "studio" || got["ManagedBy"] != "studio" || got["Name"] != "fabrica-horde-5-5-0" {
			t.Errorf("%s = %v; want Project=studio, ManagedBy=studio, Name set", name, got)
		}
	}
	packer, err := bc.renderTemplate("packer.hcl.tmpl", bc.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(packer), `"Project" = "studio"`) || !strings.Contains(string(packer), `"ManagedBy" = "studio"`) {
		t.Errorf("packer common_tags missing operator or owner tags:\n%s", packer)
	}
}

// TestDefaultBaseImageIsSSMAndPackerUsesFilter checks the default parent image
// is the SSM reference for Image Builder and a Canonical filter for Packer.
func TestDefaultBaseImageIsSSMAndPackerUsesFilter(t *testing.T) {
	bc := buildCommand{cfg: BuildConfig{
		Version:       "5.5.0",
		Install:       "docker",
		BaseImage:     amirecipe.DefaultBaseImage,
		Region:        "us-east-1",
		Name:          "fabrica-horde-5-5-0",
		IncludePacker: true,
	}}
	recipe, err := bc.renderTemplate("image-builder.json.tmpl", bc.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(recipe), `"parentImage": "ssm:/aws/service/canonical/ubuntu/server/22.04/stable/current/amd64/hvm/ebs-gp2/ami-id"`) {
		t.Errorf("recipe parentImage is not the SSM reference:\n%s", recipe)
	}
	packer, err := bc.renderTemplate("packer.hcl.tmpl", bc.cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(packer), `data "amazon-parameterstore" "ubuntu_jammy"`) || !strings.Contains(string(packer), "source_ami    = data.amazon-parameterstore.ubuntu_jammy.value") {
		t.Errorf("default Packer source should read the SSM parameter through a data source:\n%s", packer)
	}
}
