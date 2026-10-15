package export

import (
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/state"
)

func TestExtractPropertiesAppliesIAMPlacement(t *testing.T) {
	cfg := config.Defaults()
	cfg.Cloud.AWS.IAM = config.IAM{
		Path:                "/fabrica/",
		PermissionsBoundary: "arn:aws:iam::123456789012:policy/global",
	}
	cfg.Workstation.IAM.Path = "/workstation/"

	ws := buildModule(state.ModuleState{
		Name: "workstation",
		Resources: []state.ModuleResource{
			{TypeName: "AWS::IAM::Role", Identifier: "fabrica-workstation-role"},
			{TypeName: "AWS::IAM::InstanceProfile", Identifier: "fabrica-workstation-profile"},
		},
	}, cfg, "123456789012", "us-east-1")

	var role, profile map[string]any
	for _, r := range ws.Resources {
		switch r.TypeName {
		case "AWS::IAM::Role":
			role = r.Properties
		case "AWS::IAM::InstanceProfile":
			profile = r.Properties
		}
	}
	if role["Path"] != "/workstation/" {
		t.Errorf("role Path = %v, want module path", role["Path"])
	}
	if role["PermissionsBoundary"] != cfg.Cloud.AWS.IAM.PermissionsBoundary {
		t.Errorf("role boundary = %v", role["PermissionsBoundary"])
	}
	if profile["Path"] != "/workstation/" {
		t.Errorf("profile Path = %v", profile["Path"])
	}
	if _, ok := profile["PermissionsBoundary"]; ok {
		t.Fatal("instance profile must not carry a permissions boundary")
	}

	ci := buildModule(state.ModuleState{
		Name: "ci",
		Resources: []state.ModuleResource{
			{TypeName: "AWS::IAM::Role", Identifier: "fabrica-ci-codebuild"},
		},
	}, cfg, "123456789012", "us-east-1")
	for _, r := range ci.Resources {
		if r.TypeName != "AWS::IAM::Role" {
			continue
		}
		if _, ok := r.Properties["Path"]; ok {
			t.Fatalf("ci role Path = %v, want omitted", r.Properties["Path"])
		}
	}

	g := &terraformGenerator{}
	if g.tfAttributeName("Path") != "path" || g.tfAttributeName("PermissionsBoundary") != "permissions_boundary" {
		t.Fatalf("terraform names = %s %s", g.tfAttributeName("Path"), g.tfAttributeName("PermissionsBoundary"))
	}

	bad := config.Defaults()
	bad.Workstation.IAM.Path = "not-a-path"
	if _, ok := ec2ModuleIAM("workstation", bad); ok {
		t.Fatal("invalid path should not be stamped")
	}
	if _, ok := ec2ModuleIAM("deploy", cfg); ok {
		t.Fatal("deploy must not receive the EC2 IAM placement")
	}
	if _, ok := ec2ModuleIAM("workstation", nil); ok {
		t.Fatal("nil config")
	}
}
