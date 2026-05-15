package setup

import (
	"testing"

	fabricastate "github.com/jpvelasco/fabrica/internal/state"
)

func TestSetupTagsMergesStandardAndUserTags(t *testing.T) {
	tags := setupTags("test-version", map[string]string{
		"Environment": "dev",
		"ManagedBy":   "custom",
	})

	if tags["Environment"] != "dev" {
		t.Fatalf("user tag not copied")
	}
	if tags["ManagedBy"] != "custom" {
		t.Fatalf("user tag should override standard tag, got %q", tags["ManagedBy"])
	}
	if tags["FabricaVersion"] != "test-version" {
		t.Fatalf("FabricaVersion = %q", tags["FabricaVersion"])
	}
}

func TestCostResourcesPreservesPlanLabels(t *testing.T) {
	resources := costResources([]fabricastate.ResourcePlan{
		{TypeName: "AWS::S3::Bucket", Label: "S3 bucket", Identifier: "state-bucket"},
	})

	if len(resources) != 1 {
		t.Fatalf("resource count = %d, want 1", len(resources))
	}
	if resources[0].TypeName != "AWS::S3::Bucket" {
		t.Fatalf("TypeName = %q", resources[0].TypeName)
	}
	if resources[0].Name != "S3 bucket (state-bucket)" {
		t.Fatalf("Name = %q", resources[0].Name)
	}
}

func TestAllResourcesExisted(t *testing.T) {
	if !allResourcesExisted([]fabricastate.BootstrapResult{{Name: "bucket", Existed: true}}) {
		t.Fatal("expected true when every resource existed")
	}
	if allResourcesExisted([]fabricastate.BootstrapResult{{Name: "bucket", Existed: false}}) {
		t.Fatal("expected false when any resource was created")
	}
}
