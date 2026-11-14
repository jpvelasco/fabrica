package export

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/state"
)

func TestExtractPropertiesAppliesIAMToEC2Roles(t *testing.T) {
	cfg := config.Defaults()
	cfg.Cloud.AWS.IAM = config.IAM{
		Path:                "/fabrica/",
		PermissionsBoundary: "arn:aws:iam::123456789012:policy/boundary",
	}
	cfg.Workstation.IAM.Path = "/workstation/"

	role := extractProperties("workstation", state.ModuleResource{
		TypeName:   "AWS::IAM::Role",
		Identifier: "fabrica-workstation-role",
	}, cfg, exportAccount, exportRegion, "")
	if role["Path"] != "/workstation/" {
		t.Errorf("role path = %v, want /workstation/", role["Path"])
	}
	if role["PermissionsBoundary"] != cfg.Cloud.AWS.IAM.PermissionsBoundary {
		t.Errorf("boundary = %v", role["PermissionsBoundary"])
	}

	profile := extractProperties("workstation", state.ModuleResource{
		TypeName: "AWS::IAM::InstanceProfile",
	}, cfg, exportAccount, exportRegion, "")
	if profile["Path"] != "/workstation/" {
		t.Errorf("profile path = %v, want /workstation/", profile["Path"])
	}
	if _, ok := profile["PermissionsBoundary"]; ok {
		t.Errorf("instance profile must not carry a boundary: %v", profile["PermissionsBoundary"])
	}

	ci := extractProperties("ci", state.ModuleResource{TypeName: "AWS::IAM::Role"}, cfg, exportAccount, exportRegion, "")
	if _, ok := ci["Path"]; ok {
		t.Errorf("CI role inherited path %v", ci["Path"])
	}
	if _, ok := ci["PermissionsBoundary"]; ok {
		t.Errorf("CI role inherited boundary %v", ci["PermissionsBoundary"])
	}
}

func TestExtractPropertiesKeepsRecordedIAM(t *testing.T) {
	cfg := config.Defaults()
	cfg.Cloud.AWS.IAM = config.IAM{
		Path:                "/fabrica/",
		PermissionsBoundary: "arn:aws:iam::123456789012:policy/boundary",
	}
	props := extractProperties("workstation", state.ModuleResource{
		TypeName: "AWS::IAM::Role",
		Properties: map[string]string{
			"Path":                "/already/",
			"PermissionsBoundary": "arn:aws:iam::123456789012:policy/already",
		},
	}, cfg, exportAccount, exportRegion, "")
	if props["Path"] != "/already/" || props["PermissionsBoundary"] != "arn:aws:iam::123456789012:policy/already" {
		t.Fatalf("recorded IAM was replaced: %#v", props)
	}
}

func TestBuildModulesRejectsBadIAMPath(t *testing.T) {
	cfg := config.Defaults()
	cfg.Cloud.AWS.IAM.Path = "fabrica"
	_, err := buildModules(state.NewState("", ""), cfg)
	if err == nil || !strings.Contains(err.Error(), "must start and end with /") {
		t.Fatalf("err = %v", err)
	}
}

func TestTerraformAttributeNamesForIAM(t *testing.T) {
	var g terraformGenerator
	if got := g.tfAttributeName("PermissionsBoundary"); got != "permissions_boundary" {
		t.Errorf("PermissionsBoundary = %q", got)
	}
	if got := g.tfAttributeName("Path"); got != "path" {
		t.Errorf("Path = %q", got)
	}
}
