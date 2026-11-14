package export

import (
	"strings"
	"testing"

	"github.com/jpvelasco/fabrica/internal/config"
	"github.com/jpvelasco/fabrica/internal/state"
	"go.yaml.in/yaml/v3"
)

func TestCustomOwnershipExport(t *testing.T) {
	cfg := config.Defaults()
	cfg.Cloud.AWS.Identity.ManagedBy = "Studio-Tools"
	cfg.Cloud.AWS.Tags = map[string]string{"ManagedBy": "forged"}
	st := state.NewState("test-account", "us-east-1")
	st.ManagedBy = "Studio-Tools"
	for _, module := range []string{"horde", "perforce", "lore", "ddc", "workstation", "ci", "deploy"} {
		st.UpsertModule(module, "ami-test", "provisioning", []state.ModuleResource{
			{TypeName: "AWS::EC2::SecurityGroup", Identifier: module + "-sg"},
			{TypeName: "AWS::IAM::Role", Identifier: module + "-role"},
		})
	}
	for _, format := range []Format{CloudFormation, Terraform} {
		output, err := GenerateOutput(format, st, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(output), "forged") {
			t.Fatal("operator selected ownership")
		}
		if format == Terraform {
			if strings.Count(string(output), `ManagedBy = "Studio-Tools"`) != 16 {
				t.Fatalf("wrong Terraform ownership: %s", output)
			}
			continue
		}
		var template struct {
			Resources map[string]struct {
				Properties struct {
					Tags []struct {
						Key   string `yaml:"Key"`
						Value string `yaml:"Value"`
					} `yaml:"Tags"`
				} `yaml:"Properties"`
			} `yaml:"Resources"`
		}
		if err := yaml.Unmarshal(output, &template); err != nil {
			t.Fatal(err)
		}
		for id, res := range template.Resources {
			owner, marker := "", ""
			for _, tag := range res.Properties.Tags {
				if tag.Key == "ManagedBy" {
					owner = tag.Value
				}
				if tag.Key == "FabricaOwnershipVersion" {
					marker = tag.Value
				}
			}
			if owner != "Studio-Tools" {
				t.Fatalf("%s exported owner %q", id, owner)
			}
			if (id == "FabricaStateBucket" || id == "FabricaStateLockTable") && marker != "1" {
				t.Fatalf("%s lost backend ownership marker", id)
			}
		}
	}
	cfg.Cloud.AWS.Identity.ManagedBy = "invalid owner"
	if _, err := buildModules(st, cfg); err == nil {
		t.Fatal("invalid ownership accepted")
	}
}
